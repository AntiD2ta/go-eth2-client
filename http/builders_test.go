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
	"testing"

	client "github.com/attestantio/go-eth2-client"
	"github.com/attestantio/go-eth2-client/api"
	"github.com/attestantio/go-eth2-client/http"
	"github.com/attestantio/go-eth2-client/spec/gloas"
	"github.com/attestantio/go-eth2-client/spec/phase0"
	"github.com/stretchr/testify/require"
)

func TestBuildersRequest(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var method, path, contentType, body string
	server := httptest.NewServer(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/eth/v1/node/version":
			_, _ = w.Write([]byte(`{"data":{"version":"stub"}}`))
		case "/eth/v1/node/syncing":
			_, _ = w.Write([]byte(`{"data":{"head_slot":"1","sync_distance":"0","is_syncing":false,"is_optimistic":false,"el_offline":false}}`))
		default:
			b, _ := io.ReadAll(r.Body)
			method, path, contentType, body = r.Method, r.URL.Path, r.Header.Get("Content-Type"), string(b)
			_, _ = w.Write([]byte(`{"execution_optimistic":false,"finalized":true,"data":[]}`))
		}
	}))
	defer server.Close()
	service, err := http.New(ctx, http.WithAddress(server.URL))
	require.NoError(t, err)
	key := phase0.BLSPubKey{1}
	response, err := service.(client.BuildersProvider).Builders(ctx, &api.BuildersOpts{State: "head", Indices: []gloas.BuilderIndex{3}, PubKeys: []phase0.BLSPubKey{key}, Statuses: []string{"active"}})
	require.NoError(t, err)
	require.NotNil(t, response)
	require.Equal(t, "POST", method)
	require.Equal(t, "/eth/v1/beacon/states/head/builders", path)
	require.Equal(t, "application/json", contentType)
	require.JSONEq(t, `{"ids":["3","`+key.String()+`"],"statuses":["active"]}`, body)
	require.Equal(t, true, response.Metadata["finalized"])
}

func TestBuildersResponses(t *testing.T) {
	tests := []struct {
		name, state, response string
		status                int
	}{
		{name: "Empty", state: "head", response: `{"data":[]}`, status: 200},
		{name: "PreGloas", state: "genesis", response: `{"code":400,"message":"state is prior to Gloas"}`, status: 400},
		{name: "Missing", state: "head", response: `{"code":404,"message":"not found"}`, status: 404},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var body string
			server := httptest.NewServer(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/eth/v1/node/version":
					_, _ = w.Write([]byte(`{"data":{"version":"stub"}}`))
				case "/eth/v1/node/syncing":
					_, _ = w.Write([]byte(`{"data":{"head_slot":"1","sync_distance":"0","is_syncing":false,"is_optimistic":false,"el_offline":false}}`))
				default:
					b, _ := io.ReadAll(r.Body)
					body = string(b)
					w.WriteHeader(tt.status)
					_, _ = w.Write([]byte(tt.response))
				}
			}))
			defer server.Close()
			service, err := http.New(ctx, http.WithAddress(server.URL))
			require.NoError(t, err)
			response, err := service.(client.BuildersProvider).Builders(ctx, &api.BuildersOpts{State: tt.state})
			require.JSONEq(t, `{}`, body)
			if tt.status == 200 {
				require.NoError(t, err)
				require.Empty(t, response.Data)
				return
			}
			require.Nil(t, response)
			var apiErr *api.Error
			require.ErrorAs(t, err, &apiErr)
			require.Equal(t, tt.status, apiErr.StatusCode)
			require.Equal(t, "/eth/v1/beacon/states/"+tt.state+"/builders", apiErr.Endpoint)
		})
	}
}

func TestBuildersGloasLive(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	service := testService(ctx, t).(client.Service)
	requireOnGloas(ctx, t, service)
	response, err := service.(client.BuildersProvider).Builders(ctx, &api.BuildersOpts{State: "head", Indices: []gloas.BuilderIndex{3}})
	require.NoError(t, err)
	require.NotNil(t, response)
	require.NotEmpty(t, response.Data)
	for _, builder := range response.Data {
		require.EqualValues(t, 3, builder.Index)
		require.NotNil(t, builder.Builder)
	}
}
