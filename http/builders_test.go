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
	"fmt"
	"io"
	nethttp "net/http"
	"net/http/httptest"
	"testing"

	client "github.com/attestantio/go-eth2-client"
	"github.com/attestantio/go-eth2-client/api"
	"github.com/attestantio/go-eth2-client/http"
	"github.com/attestantio/go-eth2-client/spec/gloas"
	"github.com/attestantio/go-eth2-client/spec/phase0"
	"github.com/stretchr/testify/require"
)

func TestBuildersGloasRequest(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	requests := make(chan *nethttp.Request, 1)
	bodies := make(chan []byte, 1)
	server := httptest.NewServer(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/eth/v1/node/version":
			fmt.Fprint(w, `{"data":{"version":"test"}}`)
		case "/eth/v1/node/syncing":
			fmt.Fprint(w, `{"data":{"head_slot":"1","sync_distance":"0","is_syncing":false,"is_optimistic":false,"el_offline":false}}`)
		default:
			body, _ := io.ReadAll(r.Body)
			requests <- r
			bodies <- body
			fmt.Fprint(w, `{"execution_optimistic":false,"finalized":true,"data":[]}`)
		}
	}))
	defer server.Close()
	service, err := http.New(ctx, http.WithAddress(server.URL))
	require.NoError(t, err)
	key := phase0.BLSPubKey{1}
	response, err := service.(client.BuildersProvider).Builders(ctx, &api.BuildersOpts{
		State: "head", Indices: []gloas.BuilderIndex{3}, PubKeys: []phase0.BLSPubKey{key}, Statuses: []string{"active"},
	})
	require.NoError(t, err)
	require.Empty(t, response.Data)
	req := <-requests
	require.Equal(t, nethttp.MethodPost, req.Method)
	require.Equal(t, "/eth/v1/beacon/states/head/builders", req.URL.Path)
	require.Equal(t, "application/json", req.Header.Get("Content-Type"))
	var body map[string][]string
	require.NoError(t, json.Unmarshal(<-bodies, &body))
	require.Equal(t, map[string][]string{"ids": {"3", key.String()}, "statuses": {"active"}}, body)
}

func TestBuildersGloasErrorsAndUnfilteredRequest(t *testing.T) {
	for _, code := range []int{200, 400, 404} {
		t.Run(fmt.Sprint(code), func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			bodies := make(chan []byte, 1)
			server := httptest.NewServer(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/eth/v1/node/version":
					fmt.Fprint(w, `{"data":{"version":"test"}}`)
				case "/eth/v1/node/syncing":
					fmt.Fprint(w, `{"data":{"head_slot":"1","sync_distance":"0","is_syncing":false,"is_optimistic":false,"el_offline":false}}`)
				default:
					body, _ := io.ReadAll(r.Body)
					bodies <- body
					w.WriteHeader(code)
					if code == 200 {
						fmt.Fprint(w, `{"data":[]}`)
					} else {
						fmt.Fprint(w, `{"message":"error"}`)
					}
				}
			}))
			defer server.Close()
			service, err := http.New(ctx, http.WithAddress(server.URL))
			require.NoError(t, err)
			provider := service.(client.BuildersProvider)
			_, err = provider.Builders(ctx, nil)
			require.ErrorIs(t, err, client.ErrNoOptions)
			_, err = provider.Builders(ctx, &api.BuildersOpts{})
			require.ErrorIs(t, err, client.ErrInvalidOptions)
			response, err := provider.Builders(ctx, &api.BuildersOpts{State: "genesis"})
			require.JSONEq(t, `{}`, string(<-bodies))
			if code == 200 {
				require.NoError(t, err)
				require.Empty(t, response.Data)
			} else {
				var apiErr *api.Error
				require.ErrorAs(t, err, &apiErr)
				require.Equal(t, code, apiErr.StatusCode)
				require.Equal(t, nethttp.MethodPost, apiErr.Method)
				require.Equal(t, "/eth/v1/beacon/states/genesis/builders", apiErr.Endpoint)
			}
		})
	}
}

func TestBuildersGloasLive(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	service := testService(ctx, t).(client.Service)
	requireOnGloas(ctx, t, service)
	provider := service.(client.BuildersProvider)
	result, err := provider.Builders(ctx, &api.BuildersOpts{State: "head", Indices: []gloas.BuilderIndex{3}})
	if err != nil {
		var apiErr *api.Error
		require.ErrorAs(t, err, &apiErr)
		require.Equal(t, nethttp.StatusNotFound, apiErr.StatusCode)
		t.Log("Node has no builders route or head state")
		return
	}
	require.Len(t, result.Data, 1)
	builder := result.Data[0]
	require.Equal(t, gloas.BuilderIndex(3), builder.Index)
	require.NotNil(t, builder.Builder)
	filtered, err := provider.Builders(ctx, &api.BuildersOpts{State: "head", PubKeys: []phase0.BLSPubKey{builder.Builder.PublicKey}, Statuses: []string{builder.Status}})
	require.NoError(t, err)
	require.Len(t, filtered.Data, 1)
	require.Equal(t, builder, filtered.Data[0])
}
