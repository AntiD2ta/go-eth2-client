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
	"net/http"
	"net/http/httptest"
	"testing"

	eth2client "github.com/attestantio/go-eth2-client"
	"github.com/attestantio/go-eth2-client/api"
	client "github.com/attestantio/go-eth2-client/http"
	"github.com/attestantio/go-eth2-client/spec/gloas"
	"github.com/attestantio/go-eth2-client/spec/phase0"
	"github.com/stretchr/testify/require"
)

func TestBuildersPostFilters(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var method, path, body string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/eth/v1/node/syncing":
			_, _ = io.WriteString(w, `{"data":{"head_slot":"1","sync_distance":"0","is_syncing":false}}`)
		case "/eth/v1/node/version":
			_, _ = io.WriteString(w, `{"data":{"version":"test"}}`)
		default:
			method, path = r.Method, r.URL.Path
			data, _ := io.ReadAll(r.Body)
			body = string(data)
			_, _ = io.WriteString(w, `{"execution_optimistic":false,"finalized":true,"data":[]}`)
		}
	}))
	defer server.Close()
	svc, err := client.New(ctx, client.WithAddress(server.URL))
	require.NoError(t, err)
	pubkey := phase0.BLSPubKey{1}
	result, err := svc.(*client.Service).Builders(ctx, &api.BuildersOpts{State: "head", Indices: []gloas.BuilderIndex{3}, PubKeys: []phase0.BLSPubKey{pubkey}, Statuses: []string{"active"}})
	require.NoError(t, err)
	require.Empty(t, result.Data)
	require.Equal(t, "POST", method)
	require.Equal(t, "/eth/v1/beacon/states/head/builders", path)
	require.JSONEq(t, `{"ids":["3","`+pubkey.String()+`"],"statuses":["active"]}`, body)
}

func TestBuildersHTTPStatusAndEmptyFilter(t *testing.T) {
	for _, test := range []struct {
		name   string
		status int
	}{
		{name: "EmptyResult", status: http.StatusOK},
		{name: "PreGloas", status: http.StatusBadRequest},
		{name: "MissingRoute", status: http.StatusNotFound},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var method, path, body string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/eth/v1/node/syncing":
					_, _ = io.WriteString(w, `{"data":{"head_slot":"1","sync_distance":"0","is_syncing":false}}`)
				case "/eth/v1/node/version":
					_, _ = io.WriteString(w, `{"data":{"version":"test"}}`)
				default:
					method, path = r.Method, r.URL.Path
					data, _ := io.ReadAll(r.Body)
					body = string(data)
					w.WriteHeader(test.status)
					if test.status == http.StatusOK {
						_, _ = io.WriteString(w, `{"data":[]}`)
					} else {
						_, _ = io.WriteString(w, `{"code":400,"message":"not available"}`)
					}
				}
			}))
			defer server.Close()
			svc, err := client.New(ctx, client.WithAddress(server.URL))
			require.NoError(t, err)
			result, err := svc.(*client.Service).Builders(ctx, &api.BuildersOpts{State: "genesis"})
			if test.status == http.StatusOK {
				require.NoError(t, err)
				require.Empty(t, result.Data)
			} else {
				var apiErr *api.Error
				require.ErrorAs(t, err, &apiErr)
				require.Equal(t, test.status, apiErr.StatusCode)
				require.Equal(t, "/eth/v1/beacon/states/genesis/builders", apiErr.Endpoint)
				require.Nil(t, result)
			}
			require.Equal(t, "POST", method)
			require.Equal(t, "/eth/v1/beacon/states/genesis/builders", path)
			require.JSONEq(t, `{}`, body)
		})
	}
}

func TestGloasBuildersLive(t *testing.T) {
	ctx := context.Background()
	svc := testService(ctx, t)
	requireOnGloas(ctx, t, svc)
	provider := svc.(eth2client.BuildersProvider)
	response, err := provider.Builders(ctx, &api.BuildersOpts{State: "finalized", Indices: []gloas.BuilderIndex{3}})
	if err != nil {
		var apiErr *api.Error
		require.ErrorAs(t, err, &apiErr)
		require.Equal(t, http.StatusNotFound, apiErr.StatusCode)
		t.Log("node returned 404; successful builder retrieval is unsupported")
		return
	}
	require.Len(t, response.Data, 1)
	entry := response.Data[0]
	require.EqualValues(t, 3, entry.Index)
	require.NotNil(t, entry.Builder)
	filtered, err := provider.Builders(ctx, &api.BuildersOpts{State: "finalized", PubKeys: []phase0.BLSPubKey{entry.Builder.PublicKey}, Statuses: []string{entry.Status}})
	require.NoError(t, err)
	require.Equal(t, response.Data, filtered.Data)
}
