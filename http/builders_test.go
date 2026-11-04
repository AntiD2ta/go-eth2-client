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
	clienthttp "github.com/attestantio/go-eth2-client/http"
	"github.com/attestantio/go-eth2-client/spec/gloas"
	"github.com/attestantio/go-eth2-client/spec/phase0"
	"github.com/stretchr/testify/require"
)

func TestBuildersRequest(t *testing.T) {
	ctx := context.Background()
	requests := make(chan *nethttp.Request, 2)
	bodies := make(chan []byte, 2)
	server := httptest.NewServer(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/eth/v1/node/version":
			fmt.Fprint(w, `{"data":{"version":"stub"}}`)
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
	service, err := clienthttp.New(ctx, clienthttp.WithAddress(server.URL))
	require.NoError(t, err)
	provider := service.(client.BuildersProvider)
	pubkey := phase0.BLSPubKey{1}
	for _, tc := range []struct {
		opts     *api.BuildersOpts
		expected string
	}{
		{&api.BuildersOpts{State: "head"}, `{}`},
		{&api.BuildersOpts{State: "head", Indices: []gloas.BuilderIndex{3}, PubKeys: []phase0.BLSPubKey{pubkey}, Statuses: []string{"active"}}, fmt.Sprintf(`{"ids":["3",%q],"statuses":["active"]}`, pubkey.String())},
	} {
		response, err := provider.Builders(ctx, tc.opts)
		require.NoError(t, err)
		require.Empty(t, response.Data)
		require.Equal(t, true, response.Metadata["finalized"])
		require.Equal(t, false, response.Metadata["execution_optimistic"])
		req := <-requests
		require.Equal(t, "POST", req.Method)
		require.Equal(t, "/eth/v1/beacon/states/head/builders", req.URL.Path)
		require.Equal(t, "application/json", req.Header.Get("Content-Type"))
		var body map[string]any
		data := <-bodies
		require.NoError(t, json.Unmarshal(data, &body))
		require.NotNil(t, body)
		require.JSONEq(t, tc.expected, string(data))
	}
}

func TestBuildersErrors(t *testing.T) {
	ctx := context.Background()
	for _, code := range []int{400, 404} {
		t.Run(fmt.Sprint(code), func(t *testing.T) {
			server := httptest.NewServer(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/eth/v1/node/version":
					fmt.Fprint(w, `{"data":{"version":"stub"}}`)
				case "/eth/v1/node/syncing":
					fmt.Fprint(w, `{"data":{"head_slot":"1","sync_distance":"0","is_syncing":false,"is_optimistic":false,"el_offline":false}}`)
				default:
					w.WriteHeader(code)
					fmt.Fprintf(w, `{"code":%d,"message":"unavailable"}`, code)
				}
			}))
			defer server.Close()
			service, err := clienthttp.New(ctx, clienthttp.WithAddress(server.URL))
			require.NoError(t, err)
			provider := service.(client.BuildersProvider)
			response, err := provider.Builders(ctx, &api.BuildersOpts{State: "genesis"})
			require.Nil(t, response)
			var apiErr *api.Error
			require.ErrorAs(t, err, &apiErr)
			require.Equal(t, code, apiErr.StatusCode)
			require.Equal(t, "POST", apiErr.Method)
			require.Equal(t, "/eth/v1/beacon/states/genesis/builders", apiErr.Endpoint)
			_, err = provider.Builders(ctx, nil)
			require.ErrorIs(t, err, client.ErrNoOptions)
			_, err = provider.Builders(ctx, &api.BuildersOpts{})
			require.ErrorIs(t, err, client.ErrInvalidOptions)
		})
	}
}

func TestBuildersGloas(t *testing.T) {
	ctx := context.Background()
	service := testService(ctx, t).(client.Service)
	requireOnGloas(ctx, t, service)
	response, err := service.(client.BuildersProvider).Builders(ctx, &api.BuildersOpts{State: "head", Indices: []gloas.BuilderIndex{3}})
	if err != nil {
		var apiErr *api.Error
		require.ErrorAs(t, err, &apiErr)
		require.Equal(t, 404, apiErr.StatusCode)
		require.Equal(t, "/eth/v1/beacon/states/head/builders", apiErr.Endpoint)
		return
	}
	require.NotNil(t, response)
	require.LessOrEqual(t, len(response.Data), 1)
	if len(response.Data) == 1 {
		require.EqualValues(t, 3, response.Data[0].Index)
		require.NotEmpty(t, response.Data[0].Status)
		require.NotNil(t, response.Data[0].Builder)
	}
}

func TestBuildersMalformedResponse(t *testing.T) {
	ctx := context.Background()
	server := httptest.NewServer(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/eth/v1/node/version":
			fmt.Fprint(w, `{"data":{"version":"stub"}}`)
		case "/eth/v1/node/syncing":
			fmt.Fprint(w, `{"data":{"head_slot":"1","sync_distance":"0","is_syncing":false,"is_optimistic":false,"el_offline":false}}`)
		default:
			fmt.Fprint(w, `{"data":"invalid"}`)
		}
	}))
	defer server.Close()
	service, err := clienthttp.New(ctx, clienthttp.WithAddress(server.URL))
	require.NoError(t, err)
	response, err := service.(client.BuildersProvider).Builders(ctx, &api.BuildersOpts{State: "head"})
	require.Nil(t, response)
	require.ErrorContains(t, err, "failed to decode builders")
	var typeErr *json.UnmarshalTypeError
	require.ErrorAs(t, err, &typeErr)
}

func TestBuildersInactive(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	server := httptest.NewServer(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, _ *nethttp.Request) { w.WriteHeader(503) }))
	defer server.Close()
	service, err := clienthttp.New(ctx, clienthttp.WithAddress(server.URL), clienthttp.WithAllowDelayedStart(true))
	require.NoError(t, err)
	response, err := service.(client.BuildersProvider).Builders(ctx, &api.BuildersOpts{State: "head"})
	require.Nil(t, response)
	require.ErrorIs(t, err, client.ErrNotActive)
}
