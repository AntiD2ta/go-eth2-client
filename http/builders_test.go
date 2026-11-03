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
	"io"
	nethttp "net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	client "github.com/attestantio/go-eth2-client"
	"github.com/attestantio/go-eth2-client/api"
	"github.com/attestantio/go-eth2-client/http"
	"github.com/stretchr/testify/require"
)

func TestBuildersPOST(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	type request struct{ method, path, body, contentType string }
	requests := make(chan request, 1)
	server := httptest.NewServer(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
		switch r.URL.Path {
		case "/eth/v1/node/version":
			_, _ = w.Write([]byte(`{"data":{"version":"test"}}`))
		case "/eth/v1/node/syncing":
			_, _ = w.Write([]byte(`{"data":{"is_syncing":false,"is_optimistic":false,"el_offline":false,"head_slot":"1","sync_distance":"0"}}`))
		default:
			body, _ := io.ReadAll(r.Body)
			requests <- request{r.Method, r.URL.Path, string(body), r.Header.Get("Content-Type")}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"execution_optimistic":false,"finalized":true,"data":[]}`))
		}
	}))
	defer server.Close()
	service, err := http.New(ctx, http.WithAddress(server.URL))
	require.NoError(t, err)
	provider := service.(client.BuildersProvider)
	for _, tc := range []struct {
		name string
		opts *api.BuildersOpts
		body string
	}{
		{"Unfiltered", &api.BuildersOpts{State: "head"}, `{}`},
		{"Filters", &api.BuildersOpts{State: "head", IDs: []string{"3", "0x" + strings.Repeat("11", 48)}, Statuses: []string{"active"}}, `{"ids":["3","0x` + strings.Repeat("11", 48) + `"],"statuses":["active"]}`},
		{"StatusOnly", &api.BuildersOpts{State: "head", Statuses: []string{"pending"}}, `{"statuses":["pending"]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			response, err := provider.Builders(ctx, tc.opts)
			require.NoError(t, err)
			require.Empty(t, response.Data)
			require.Equal(t, true, response.Metadata["finalized"])
			got := <-requests
			require.Equal(t, "POST", got.method)
			require.Equal(t, "/eth/v1/beacon/states/head/builders", got.path)
			require.Equal(t, "application/json", got.contentType)
			require.JSONEq(t, tc.body, got.body)
		})
	}
}

func TestBuildersErrors(t *testing.T) {
	for _, status := range []int{nethttp.StatusBadRequest, nethttp.StatusNotFound, nethttp.StatusOK} {
		t.Run(nethttp.StatusText(status), func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			server := httptest.NewServer(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
				switch r.URL.Path {
				case "/eth/v1/node/version":
					_, _ = w.Write([]byte(`{"data":{"version":"test"}}`))
				case "/eth/v1/node/syncing":
					_, _ = w.Write([]byte(`{"data":{"is_syncing":false,"is_optimistic":false,"el_offline":false,"head_slot":"1","sync_distance":"0"}}`))
				default:
					w.WriteHeader(status)
					if status == nethttp.StatusOK {
						_, _ = w.Write([]byte(`{"data":`))
					} else {
						_, _ = w.Write([]byte(`{"message":"not available"}`))
					}
				}
			}))
			defer server.Close()
			service, err := http.New(ctx, http.WithAddress(server.URL))
			require.NoError(t, err)
			result, err := service.(client.BuildersProvider).Builders(ctx, &api.BuildersOpts{State: "genesis"})
			require.Nil(t, result)
			if status == nethttp.StatusOK {
				require.ErrorContains(t, err, "failed to parse JSON")
				return
			}
			var apiErr *api.Error
			require.ErrorAs(t, err, &apiErr)
			require.Equal(t, status, apiErr.StatusCode)
			require.Equal(t, "POST", apiErr.Method)
			require.Equal(t, "/eth/v1/beacon/states/genesis/builders", apiErr.Endpoint)
		})
	}
}

func TestGloasBuildersLive(t *testing.T) {
	ctx := context.Background()
	service := testService(ctx, t)
	requireOnGloas(ctx, t, service)
	result, err := service.(client.BuildersProvider).Builders(ctx, &api.BuildersOpts{State: "head", IDs: []string{"3"}})
	require.NoError(t, err)
	require.Len(t, result.Data, 1)
	require.EqualValues(t, 3, result.Data[0].Index)
	require.NotNil(t, result.Data[0].Builder)
}

func TestBuildersOptions(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var stopped atomic.Bool
	server := httptest.NewServer(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
		if stopped.Load() {
			w.WriteHeader(nethttp.StatusServiceUnavailable)
			return
		}
		switch r.URL.Path {
		case "/eth/v1/node/version":
			_, _ = w.Write([]byte(`{"data":{"version":"test"}}`))
		case "/eth/v1/node/syncing":
			_, _ = w.Write([]byte(`{"data":{"is_syncing":false,"is_optimistic":false,"el_offline":false,"head_slot":"1","sync_distance":"0"}}`))
		default:
			t.Errorf("unexpected request to %s", r.URL.Path)
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
	stopped.Store(true)
	service.(*http.Service).CheckConnectionState(ctx)
	_, err = provider.Builders(ctx, &api.BuildersOpts{State: "head"})
	require.ErrorIs(t, err, client.ErrNotActive)
}
