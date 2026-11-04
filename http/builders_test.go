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
	"encoding/json"
	nethttp "net/http"
	"net/http/httptest"
	"testing"

	client "github.com/attestantio/go-eth2-client"
	"github.com/attestantio/go-eth2-client/api"
	apiv1 "github.com/attestantio/go-eth2-client/api/v1"
	clienthttp "github.com/attestantio/go-eth2-client/http"
	"github.com/attestantio/go-eth2-client/spec/gloas"
	"github.com/attestantio/go-eth2-client/spec/phase0"
	"github.com/stretchr/testify/require"
)

func TestBuildersPostsObject(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pubkey := phase0.BLSPubKey{1}
	tests := []struct {
		name string
		opts *api.BuildersOpts
		want map[string]any
	}{
		{"All", &api.BuildersOpts{State: "head"}, map[string]any{}},
		{"Filtered", &api.BuildersOpts{State: "head", Indices: []gloas.BuilderIndex{3}, PubKeys: []phase0.BLSPubKey{pubkey}, Statuses: []apiv1.BuilderStatus{apiv1.BuilderStatusActive}}, map[string]any{"ids": []any{"3", pubkey.String()}, "statuses": []any{"active"}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			received := make(chan map[string]any, 1)
			server := httptest.NewServer(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
				switch r.URL.Path {
				case "/eth/v1/node/version":
					_, _ = w.Write([]byte(`{"data":{"version":"test"}}`))
				case "/eth/v1/node/syncing":
					_, _ = w.Write([]byte(`{"data":{"is_syncing":false,"is_optimistic":false,"el_offline":false,"head_slot":"1","sync_distance":"0"}}`))
				case "/eth/v1/beacon/states/head/builders":
					if r.Method != nethttp.MethodPost || r.Header.Get("Content-Type") != "application/json" {
						w.WriteHeader(nethttp.StatusBadRequest)
						return
					}
					var body map[string]any
					if json.NewDecoder(r.Body).Decode(&body) != nil || body == nil {
						w.WriteHeader(nethttp.StatusBadRequest)
						return
					}
					received <- body
					_, _ = w.Write([]byte(`{"execution_optimistic":false,"finalized":true,"data":[]}`))
				default:
					w.WriteHeader(nethttp.StatusNotFound)
				}
			}))
			defer server.Close()
			service, err := clienthttp.New(ctx, clienthttp.WithAddress(server.URL))
			require.NoError(t, err)
			res, err := service.(client.BuildersProvider).Builders(ctx, test.opts)
			require.NoError(t, err)
			require.Equal(t, test.want, <-received)
			require.Empty(t, res.Data)
			require.Equal(t, true, res.Metadata["finalized"])
		})
	}
}

func TestBuildersErrors(t *testing.T) {
	for _, status := range []int{nethttp.StatusBadRequest, nethttp.StatusNotFound} {
		t.Run(nethttp.StatusText(status), func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			received := make(chan bool, 1)
			server := httptest.NewServer(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
				switch r.URL.Path {
				case "/eth/v1/node/version":
					_, _ = w.Write([]byte(`{"data":{"version":"test"}}`))
				case "/eth/v1/node/syncing":
					_, _ = w.Write([]byte(`{"data":{"is_syncing":false,"is_optimistic":false,"el_offline":false,"head_slot":"1","sync_distance":"0"}}`))
				case "/eth/v1/beacon/states/genesis/builders":
					received <- true
					w.WriteHeader(status)
				default:
					w.WriteHeader(nethttp.StatusNotFound)
				}
			}))
			defer server.Close()
			service, err := clienthttp.New(ctx, clienthttp.WithAddress(server.URL))
			require.NoError(t, err)
			res, err := service.(client.BuildersProvider).Builders(ctx, &api.BuildersOpts{State: "genesis"})
			require.Nil(t, res)
			var apiErr *api.Error
			require.ErrorAs(t, err, &apiErr)
			require.Equal(t, status, apiErr.StatusCode)
			require.Equal(t, nethttp.MethodPost, apiErr.Method)
			require.Equal(t, "/eth/v1/beacon/states/genesis/builders", apiErr.Endpoint)
			require.True(t, <-received)
		})
	}
}

func TestBuildersInvalidOptions(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	server := httptest.NewServer(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
		switch r.URL.Path {
		case "/eth/v1/node/version":
			_, _ = w.Write([]byte(`{"data":{"version":"test"}}`))
		case "/eth/v1/node/syncing":
			_, _ = w.Write([]byte(`{"data":{"is_syncing":false,"is_optimistic":false,"el_offline":false,"head_slot":"1","sync_distance":"0"}}`))
		default:
			w.WriteHeader(nethttp.StatusNotFound)
		}
	}))
	defer server.Close()
	service, err := clienthttp.New(ctx, clienthttp.WithAddress(server.URL))
	require.NoError(t, err)
	provider := service.(client.BuildersProvider)
	_, err = provider.Builders(ctx, nil)
	require.ErrorIs(t, err, client.ErrNoOptions)
	_, err = provider.Builders(ctx, &api.BuildersOpts{})
	require.ErrorIs(t, err, client.ErrInvalidOptions)
}

func TestGloasBuildersLive(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	service := testService(ctx, t).(client.Service)
	requireOnGloas(ctx, t, service)
	provider := service.(client.BuildersProvider)
	all, err := provider.Builders(ctx, &api.BuildersOpts{State: "finalized"})
	if err != nil {
		var apiErr *api.Error
		require.ErrorAs(t, err, &apiErr)
		require.Equal(t, nethttp.StatusNotFound, apiErr.StatusCode)
		require.Equal(t, "/eth/v1/beacon/states/finalized/builders", apiErr.Endpoint)
		t.Log("node does not support the builders route or lacks the requested state")
		return
	}
	require.NotEmpty(t, all.Data)
	first := all.Data[0]
	require.NotNil(t, first.Builder)
	filtered, err := provider.Builders(ctx, &api.BuildersOpts{State: "finalized", Indices: []gloas.BuilderIndex{first.Index}, Statuses: []apiv1.BuilderStatus{first.Status}})
	require.NoError(t, err)
	require.Equal(t, []*apiv1.Builder{first}, filtered.Data)
	byKey, err := provider.Builders(ctx, &api.BuildersOpts{State: "finalized", PubKeys: []phase0.BLSPubKey{first.Builder.PublicKey}})
	require.NoError(t, err)
	require.Equal(t, []*apiv1.Builder{first}, byKey.Data)
}

func TestBuildersRejectMalformedResponse(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	server := httptest.NewServer(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
		switch r.URL.Path {
		case "/eth/v1/node/version":
			_, _ = w.Write([]byte(`{"data":{"version":"test"}}`))
		case "/eth/v1/node/syncing":
			_, _ = w.Write([]byte(`{"data":{"is_syncing":false,"is_optimistic":false,"el_offline":false,"head_slot":"1","sync_distance":"0"}}`))
		case "/eth/v1/beacon/states/head/builders":
			_, _ = w.Write([]byte(`{"data":[{}]}`))
		default:
			w.WriteHeader(nethttp.StatusNotFound)
		}
	}))
	defer server.Close()
	service, err := clienthttp.New(ctx, clienthttp.WithAddress(server.URL))
	require.NoError(t, err)
	response, err := service.(client.BuildersProvider).Builders(ctx, &api.BuildersOpts{State: "head"})
	require.Nil(t, response)
	require.ErrorContains(t, err, "index missing")
}
