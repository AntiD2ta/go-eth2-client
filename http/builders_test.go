// Copyright © 2026 Attestant Limited.
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package http_test

import (
	"context"
	"errors"
	"io"
	nethttp "net/http"
	"net/http/httptest"
	"testing"

	client "github.com/attestantio/go-eth2-client"
	"github.com/attestantio/go-eth2-client/api"
	apiv1 "github.com/attestantio/go-eth2-client/api/v1"
	ethhttp "github.com/attestantio/go-eth2-client/http"
	"github.com/attestantio/go-eth2-client/spec/gloas"
	"github.com/attestantio/go-eth2-client/spec/phase0"
	"github.com/stretchr/testify/require"
)

func TestBuildersRequest(t *testing.T) {
	requests := make(chan *nethttp.Request, 1)
	bodies := make(chan string, 1)
	server := httptest.NewServer(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
		switch r.URL.Path {
		case "/eth/v1/node/version":
			_, _ = w.Write([]byte(`{"data":{"version":"test"}}`))
		case "/eth/v1/node/syncing":
			_, _ = w.Write([]byte(`{"data":{"is_syncing":false,"is_optimistic":false,"el_offline":false,"head_slot":"1","sync_distance":"0"}}`))
		default:
			data, _ := io.ReadAll(r.Body)
			requests <- r
			bodies <- string(data)
			_, _ = w.Write([]byte(`{"execution_optimistic":false,"finalized":true,"data":[]}`))
		}
	}))
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	service, err := ethhttp.New(ctx, ethhttp.WithAddress(server.URL))
	require.NoError(t, err)
	key := phase0.BLSPubKey{1}
	res, err := service.(client.BuildersProvider).Builders(ctx, &api.BuildersOpts{State: "head", Indices: []gloas.BuilderIndex{3}, PubKeys: []phase0.BLSPubKey{key}, Statuses: []apiv1.BuilderStatus{apiv1.BuilderStatusActive}})
	require.NoError(t, err)
	require.Empty(t, res.Data)
	require.Equal(t, true, res.Metadata["finalized"])
	req := <-requests
	require.Equal(t, nethttp.MethodPost, req.Method)
	require.Equal(t, "/eth/v1/beacon/states/head/builders", req.URL.Path)
	require.Equal(t, "application/json", req.Header.Get("Content-Type"))
	require.JSONEq(t, `{"ids":["3","`+key.String()+`"],"statuses":["active"]}`, <-bodies)
}

func TestBuildersErrorsAndUnfilteredBody(t *testing.T) {
	for _, status := range []int{nethttp.StatusOK, nethttp.StatusBadRequest, nethttp.StatusNotFound} {
		t.Run(nethttp.StatusText(status), func(t *testing.T) {
			body := make(chan string, 1)
			server := httptest.NewServer(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
				switch r.URL.Path {
				case "/eth/v1/node/version":
					_, _ = w.Write([]byte(`{"data":{"version":"test"}}`))
				case "/eth/v1/node/syncing":
					_, _ = w.Write([]byte(`{"data":{"is_syncing":false,"is_optimistic":false,"el_offline":false,"head_slot":"1","sync_distance":"0"}}`))
				default:
					data, _ := io.ReadAll(r.Body)
					body <- string(data)
					w.WriteHeader(status)
					if status == nethttp.StatusOK {
						_, _ = w.Write([]byte(`{"execution_optimistic":false,"finalized":false,"data":[]}`))
					} else {
						_, _ = w.Write([]byte(`{"code":400,"message":"error"}`))
					}
				}
			}))
			defer server.Close()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			service, err := ethhttp.New(ctx, ethhttp.WithAddress(server.URL))
			require.NoError(t, err)
			provider := service.(client.BuildersProvider)
			_, err = provider.Builders(ctx, nil)
			require.ErrorIs(t, err, client.ErrNoOptions)
			_, err = provider.Builders(ctx, &api.BuildersOpts{})
			require.ErrorIs(t, err, client.ErrInvalidOptions)
			res, err := provider.Builders(ctx, &api.BuildersOpts{State: "genesis"})
			if status == nethttp.StatusOK {
				require.NoError(t, err)
				require.NotNil(t, res)
				require.Empty(t, res.Data)
			} else {
				require.Nil(t, res)
				var apiErr *api.Error
				require.ErrorAs(t, err, &apiErr)
				require.Equal(t, status, apiErr.StatusCode)
				require.Equal(t, nethttp.MethodPost, apiErr.Method)
				require.Equal(t, "/eth/v1/beacon/states/genesis/builders", apiErr.Endpoint)
			}
			require.JSONEq(t, `{}`, <-body)
		})
	}
}

func TestBuildersGloasLive(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	service := testService(ctx, t).(client.Service)
	requireOnGloas(ctx, t, service)
	provider := service.(client.BuildersProvider)
	all, err := provider.Builders(ctx, &api.BuildersOpts{State: "finalized"})
	if err != nil {
		var apiErr *api.Error
		if errors.As(err, &apiErr) && apiErr.StatusCode == nethttp.StatusNotFound {
			require.Equal(t, nethttp.MethodPost, apiErr.Method)
			require.Equal(t, "/eth/v1/beacon/states/finalized/builders", apiErr.Endpoint)
			t.Log("node reports 404 for builders")
			return
		}
	}
	require.NoError(t, err)
	require.NotEmpty(t, all.Data, "the live node needs registered builders to verify filters")
	first := all.Data[0]
	require.NotNil(t, first.Builder)
	for _, opts := range []*api.BuildersOpts{
		{State: "finalized", Indices: []gloas.BuilderIndex{first.Index}},
		{State: "finalized", PubKeys: []phase0.BLSPubKey{first.Builder.PublicKey}},
		{State: "finalized", Statuses: []apiv1.BuilderStatus{first.Status}},
	} {
		res, err := provider.Builders(ctx, opts)
		require.NoError(t, err)
		require.Contains(t, res.Data, first)
		for _, b := range res.Data {
			if len(opts.Indices) > 0 {
				require.Equal(t, first.Index, b.Index)
			}
			if len(opts.PubKeys) > 0 {
				require.Equal(t, first.Builder.PublicKey, b.Builder.PublicKey)
			}
			if len(opts.Statuses) > 0 {
				require.Equal(t, first.Status, b.Status)
			}
		}
	}
}

func TestBuildersInvalidResponse(t *testing.T) {
	server := httptest.NewServer(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
		switch r.URL.Path {
		case "/eth/v1/node/version":
			_, _ = w.Write([]byte(`{"data":{"version":"test"}}`))
		case "/eth/v1/node/syncing":
			_, _ = w.Write([]byte(`{"data":{"is_syncing":false,"is_optimistic":false,"el_offline":false,"head_slot":"1","sync_distance":"0"}}`))
		default:
			_, _ = w.Write([]byte(`{"data":[{"index":"bad"}]}`))
		}
	}))
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	service, err := ethhttp.New(ctx, ethhttp.WithAddress(server.URL))
	require.NoError(t, err)
	res, err := service.(client.BuildersProvider).Builders(ctx, &api.BuildersOpts{State: "head"})
	require.Nil(t, res)
	require.ErrorContains(t, err, "failed to unmarshal data")
}
