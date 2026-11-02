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
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	client "github.com/attestantio/go-eth2-client"
	"github.com/attestantio/go-eth2-client/api"
	apiv1 "github.com/attestantio/go-eth2-client/api/v1"
	eth2http "github.com/attestantio/go-eth2-client/http"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/require"
)

func TestBuildersRequest(t *testing.T) {
	var received bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/eth/v1/node/syncing":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":{"head_slot":"0","sync_distance":"0","is_optimistic":false,"is_syncing":false}}`))
		case "/eth/v1/node/version":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":{"version":"test"}}`))
		case "/eth/v1/beacon/states/head/builders":
			received = true
			require.Equal(t, http.MethodPost, r.Method)
			require.Equal(t, "application/json", r.Header.Get("Content-Type"))
			var body map[string]json.RawMessage
			require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
			require.NotNil(t, body)
			require.Empty(t, body)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"execution_optimistic":false,"finalized":false,"data":[]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	service, err := eth2http.New(ctx, eth2http.WithAddress(server.URL), eth2http.WithAllowDelayedStart(true), eth2http.WithLogLevel(zerolog.Disabled))
	require.NoError(t, err)
	response, err := service.(client.BuildersProvider).Builders(ctx, &api.BuildersOpts{State: "head"})
	require.NoError(t, err)
	require.True(t, received)
	require.Empty(t, response.Data)
}

func TestBuildersFiltersAndErrors(t *testing.T) {
	for _, status := range []int{http.StatusOK, http.StatusBadRequest, http.StatusNotFound} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			requests := make(chan map[string][]string, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/eth/v1/node/syncing":
					w.Header().Set("Content-Type", "application/json")
					_, _ = w.Write([]byte(`{"data":{"head_slot":"0","sync_distance":"0","is_optimistic":false,"is_syncing":false}}`))
				case "/eth/v1/node/version":
					w.Header().Set("Content-Type", "application/json")
					_, _ = w.Write([]byte(`{"data":{"version":"test"}}`))
				case "/eth/v1/beacon/states/finalized/builders":
					require.Equal(t, http.MethodPost, r.Method)
					var body map[string][]string
					require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
					requests <- body
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(status)
					if status == http.StatusOK {
						_, _ = w.Write([]byte(`{"execution_optimistic":true,"finalized":true,"data":[]}`))
					} else {
						_, _ = w.Write([]byte(`{"code":400,"message":"test"}`))
					}
				default:
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			service, err := eth2http.New(ctx, eth2http.WithAddress(server.URL), eth2http.WithAllowDelayedStart(true), eth2http.WithLogLevel(zerolog.Disabled))
			require.NoError(t, err)
			key := "0x" + strings.Repeat("12", 48)
			response, err := service.(client.BuildersProvider).Builders(ctx, &api.BuildersOpts{State: "finalized", IDs: []string{"3", key}, Statuses: []apiv1.BuilderStatus{apiv1.BuilderStatusActive, apiv1.BuilderStatusExited}})
			if status == http.StatusOK {
				require.NoError(t, err)
				require.Empty(t, response.Data)
				require.Equal(t, true, response.Metadata["finalized"])
			} else {
				require.Nil(t, response)
				var apiErr *api.Error
				require.ErrorAs(t, err, &apiErr)
				require.Equal(t, status, apiErr.StatusCode)
				require.Equal(t, http.MethodPost, apiErr.Method)
				require.Equal(t, "/eth/v1/beacon/states/finalized/builders", apiErr.Endpoint)
			}
			require.Equal(t, map[string][]string{"ids": {"3", key}, "statuses": {"active", "exited"}}, <-requests)
		})
	}
}

func TestBuildersGloas(t *testing.T) {
	ctx := context.Background()
	service := testService(ctx, t)
	requireOnGloas(ctx, t, service)
	provider := service.(client.BuildersProvider)
	response, err := provider.Builders(ctx, &api.BuildersOpts{State: "head"})
	if err != nil {
		var apiErr *api.Error
		require.ErrorAs(t, err, &apiErr)
		require.Equal(t, http.StatusNotFound, apiErr.StatusCode)
		t.Log("Node does not expose the builders endpoint")
		return
	}
	require.NotNil(t, response)
	if len(response.Data) == 0 {
		t.Log("No registered builders to filter")
		return
	}
	first := response.Data[0]
	require.NotNil(t, first.Builder)
	filtered, err := provider.Builders(ctx, &api.BuildersOpts{State: "head", IDs: []string{fmt.Sprint(first.Index)}})
	require.NoError(t, err)
	require.Len(t, filtered.Data, 1)
	require.Equal(t, first.Index, filtered.Data[0].Index)
}
