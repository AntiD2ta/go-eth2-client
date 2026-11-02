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
	"net/http"
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

func TestBuildersLocalRequest(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	type request struct {
		method, path, contentType string
		body                      map[string][]string
	}
	requests := make(chan request, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/eth/v1/node/syncing":
			_, _ = w.Write([]byte(`{"data":{"head_slot":"1","sync_distance":"0","is_syncing":false}}`))
		case "/eth/v1/node/version":
			_, _ = w.Write([]byte(`{"data":{"version":"test"}}`))
		default:
			var body map[string][]string
			_ = json.NewDecoder(r.Body).Decode(&body)
			requests <- request{r.Method, r.URL.Path, r.Header.Get("Content-Type"), body}
			_, _ = w.Write([]byte(`{"execution_optimistic":false,"finalized":true,"data":[]}`))
		}
	}))
	defer server.Close()
	service, err := ethhttp.New(ctx, ethhttp.WithAddress(server.URL))
	require.NoError(t, err)
	key := phase0.BLSPubKey{1}
	response, err := service.(client.BuildersProvider).Builders(ctx, &api.BuildersOpts{State: "head", Indices: []gloas.BuilderIndex{3}, PubKeys: []phase0.BLSPubKey{key}, Statuses: []apiv1.BuilderStatus{apiv1.BuilderStatusActive}})
	require.NoError(t, err)
	require.Empty(t, response.Data)
	got := <-requests
	require.Equal(t, http.MethodPost, got.method)
	require.Equal(t, "/eth/v1/beacon/states/head/builders", got.path)
	require.Equal(t, "application/json", got.contentType)
	require.Equal(t, map[string][]string{"ids": {"3", key.String()}, "statuses": {"active"}}, got.body)
}

func TestBuildersLocalErrorsAndEmptyFilters(t *testing.T) {
	tests := []struct {
		name   string
		status int
		state  string
	}{
		{name: "EmptyFilters", status: http.StatusOK, state: "head"},
		{name: "PreGloas", status: http.StatusBadRequest, state: "genesis"},
		{name: "NotFound", status: http.StatusNotFound, state: "head"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			requests := make(chan map[string]json.RawMessage, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/eth/v1/node/syncing":
					_, _ = w.Write([]byte(`{"data":{"head_slot":"1","sync_distance":"0","is_syncing":false}}`))
				case "/eth/v1/node/version":
					_, _ = w.Write([]byte(`{"data":{"version":"test"}}`))
				default:
					var body map[string]json.RawMessage
					_ = json.NewDecoder(r.Body).Decode(&body)
					requests <- body
					w.WriteHeader(tc.status)
					if tc.status == http.StatusOK {
						_, _ = w.Write([]byte(`{"execution_optimistic":false,"finalized":true,"data":[]}`))
					} else {
						_, _ = w.Write([]byte(`{"code":400,"message":"error"}`))
					}
				}
			}))
			defer server.Close()
			service, err := ethhttp.New(ctx, ethhttp.WithAddress(server.URL))
			require.NoError(t, err)
			response, err := service.(client.BuildersProvider).Builders(ctx, &api.BuildersOpts{State: tc.state})
			if tc.status == http.StatusOK {
				require.NoError(t, err)
				require.Empty(t, response.Data)
				require.Equal(t, true, response.Metadata["finalized"])
			} else {
				require.Nil(t, response)
				var apiErr *api.Error
				require.ErrorAs(t, err, &apiErr)
				require.Equal(t, tc.status, apiErr.StatusCode)
				require.Equal(t, http.MethodPost, apiErr.Method)
				require.Equal(t, "/eth/v1/beacon/states/"+tc.state+"/builders", apiErr.Endpoint)
			}
			body := <-requests
			require.NotNil(t, body)
			require.Empty(t, body)
		})
	}
}

func TestBuildersLocalInvalidOptions(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/eth/v1/node/syncing" {
			_, _ = w.Write([]byte(`{"data":{"head_slot":"1","sync_distance":"0","is_syncing":false}}`))
		} else {
			_, _ = w.Write([]byte(`{"data":{"version":"test"}}`))
		}
	}))
	defer server.Close()
	service, err := ethhttp.New(ctx, ethhttp.WithAddress(server.URL))
	require.NoError(t, err)
	_, err = service.(client.BuildersProvider).Builders(ctx, nil)
	require.ErrorIs(t, err, client.ErrNoOptions)
	_, err = service.(client.BuildersProvider).Builders(ctx, &api.BuildersOpts{})
	require.ErrorIs(t, err, client.ErrInvalidOptions)
}
