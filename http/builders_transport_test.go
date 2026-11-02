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
	"testing"

	client "github.com/attestantio/go-eth2-client"
	"github.com/attestantio/go-eth2-client/api"
	eth2http "github.com/attestantio/go-eth2-client/http"
	"github.com/attestantio/go-eth2-client/spec/phase0"
	"github.com/stretchr/testify/require"
)

func TestBuildersTransport(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	key := phase0.BLSPubKey{1}.String()
	requests := make(chan string, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/eth/v1/node/version":
			fmt.Fprint(w, `{"data":{"version":"stub"}}`)
		case "/eth/v1/node/syncing":
			fmt.Fprint(w, `{"data":{"head_slot":"64","sync_distance":"0","is_syncing":false,"is_optimistic":false}}`)
		case "/eth/v1/beacon/states/head/builders":
			require.Equal(t, http.MethodPost, r.Method)
			var body map[string]any
			require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
			require.Equal(t, []any{"3", key}, body["ids"])
			require.Equal(t, []any{"active"}, body["statuses"])
			requests <- r.URL.Path
			fmt.Fprint(w, `{"execution_optimistic":false,"finalized":true,"data":[]}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	service, err := eth2http.New(ctx, eth2http.WithAddress(server.URL))
	require.NoError(t, err)
	provider, ok := service.(client.BuildersProvider)
	require.True(t, ok)
	response, err := provider.Builders(ctx, &api.BuildersOpts{State: "head", IDs: []string{"3", key}, Statuses: []string{"active"}})
	require.NoError(t, err)
	require.Empty(t, response.Data)
	require.Equal(t, true, response.Metadata["finalized"])
	require.Equal(t, "/eth/v1/beacon/states/head/builders", <-requests)
}

func TestBuildersEmptyFiltersAndErrors(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/eth/v1/node/version":
			fmt.Fprint(w, `{"data":{"version":"stub"}}`)
		case "/eth/v1/node/syncing":
			fmt.Fprint(w, `{"data":{"head_slot":"64","sync_distance":"0","is_syncing":false,"is_optimistic":false}}`)
		case "/eth/v1/beacon/states/head/builders", "/eth/v1/beacon/states/genesis/builders", "/eth/v1/beacon/states/unknown/builders":
			require.Equal(t, http.MethodPost, r.Method)
			var body map[string]any
			require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
			require.NotNil(t, body)
			require.Empty(t, body)
			switch r.URL.Path {
			case "/eth/v1/beacon/states/genesis/builders":
				w.WriteHeader(http.StatusBadRequest)
			case "/eth/v1/beacon/states/unknown/builders":
				w.WriteHeader(http.StatusNotFound)
			default:
				fmt.Fprint(w, `{"execution_optimistic":false,"finalized":false,"data":[]}`)
			}
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	service, err := eth2http.New(ctx, eth2http.WithAddress(server.URL))
	require.NoError(t, err)
	provider := service.(client.BuildersProvider)
	res, err := provider.Builders(ctx, &api.BuildersOpts{State: "head"})
	require.NoError(t, err)
	require.NotNil(t, res)
	require.Empty(t, res.Data)
	for _, tc := range []struct {
		state  string
		status int
	}{{"genesis", 400}, {"unknown", 404}} {
		res, err := provider.Builders(ctx, &api.BuildersOpts{State: tc.state})
		require.Nil(t, res)
		var apiErr *api.Error
		require.ErrorAs(t, err, &apiErr)
		require.Equal(t, tc.status, apiErr.StatusCode)
		require.Equal(t, http.MethodPost, apiErr.Method)
		require.Equal(t, "/eth/v1/beacon/states/"+tc.state+"/builders", apiErr.Endpoint)
	}
}
