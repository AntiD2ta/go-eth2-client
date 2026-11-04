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
	apiv1 "github.com/attestantio/go-eth2-client/api/v1"
	"github.com/attestantio/go-eth2-client/http"
	"github.com/attestantio/go-eth2-client/spec/gloas"
	"github.com/attestantio/go-eth2-client/spec/phase0"
	"github.com/stretchr/testify/require"
)

func TestBuildersPostObject(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	type request struct {
		method, path, contentType string
		body                      []byte
		err                       error
	}
	requests := make(chan request, 2)
	server := httptest.NewServer(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/eth/v1/node/version":
			_, _ = w.Write([]byte(`{"data":{"version":"test"}}`))
		case "/eth/v1/node/syncing":
			_, _ = w.Write([]byte(`{"data":{"is_syncing":false,"is_optimistic":false,"el_offline":false,"head_slot":"1","sync_distance":"0"}}`))
		default:
			body, err := io.ReadAll(r.Body)
			requests <- request{r.Method, r.URL.Path, r.Header.Get("Content-Type"), body, err}
			_, _ = w.Write([]byte(`{"execution_optimistic":false,"finalized":true,"data":[]}`))
		}
	}))
	defer server.Close()
	service, err := http.New(ctx, http.WithAddress(server.URL))
	require.NoError(t, err)
	provider := service.(client.BuildersProvider)
	key := phase0.BLSPubKey{1}
	cases := []struct {
		opts     *api.BuildersOpts
		expected string
	}{
		{&api.BuildersOpts{State: "head"}, `{}`},
		{&api.BuildersOpts{State: "head", Statuses: []apiv1.BuilderStatus{apiv1.BuilderStatusPending}}, `{"statuses":["pending"]}`},
		{&api.BuildersOpts{State: "finalized", Indices: []gloas.BuilderIndex{3}, PubKeys: []phase0.BLSPubKey{key}, Statuses: []apiv1.BuilderStatus{apiv1.BuilderStatusActive}}, `{"ids":["3","` + key.String() + `"],"statuses":["active"]}`},
	}
	for _, tc := range cases {
		response, callErr := provider.Builders(ctx, tc.opts)
		require.NoError(t, callErr)
		require.Empty(t, response.Data)
		require.Equal(t, true, response.Metadata["finalized"])
		got := <-requests
		require.NoError(t, got.err)
		require.Equal(t, nethttp.MethodPost, got.method)
		require.Equal(t, "/eth/v1/beacon/states/"+tc.opts.State+"/builders", got.path)
		require.Equal(t, "application/json", got.contentType)
		require.JSONEq(t, tc.expected, string(got.body))
	}
}

func TestBuildersHTTPErrors(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	server := httptest.NewServer(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/eth/v1/node/version":
			_, _ = w.Write([]byte(`{"data":{"version":"test"}}`))
		case "/eth/v1/node/syncing":
			_, _ = w.Write([]byte(`{"data":{"is_syncing":false,"is_optimistic":false,"el_offline":false,"head_slot":"1","sync_distance":"0"}}`))
		case "/eth/v1/beacon/states/genesis/builders":
			w.WriteHeader(nethttp.StatusBadRequest)
		case "/eth/v1/beacon/states/head/builders":
			w.WriteHeader(nethttp.StatusNotFound)
		default:
			w.WriteHeader(nethttp.StatusInternalServerError)
		}
	}))
	defer server.Close()
	service, err := http.New(ctx, http.WithAddress(server.URL))
	require.NoError(t, err)
	for _, tc := range []struct {
		state string
		code  int
	}{{"genesis", 400}, {"head", 404}} {
		response, callErr := service.(client.BuildersProvider).Builders(ctx, &api.BuildersOpts{State: tc.state})
		require.Nil(t, response)
		var apiErr *api.Error
		require.ErrorAs(t, callErr, &apiErr)
		require.Equal(t, tc.code, apiErr.StatusCode)
		require.Equal(t, nethttp.MethodPost, apiErr.Method)
		require.Equal(t, "/eth/v1/beacon/states/"+tc.state+"/builders", apiErr.Endpoint)
	}
}

func TestBuildersGloas(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	service := testService(ctx, t).(client.Service)
	requireOnGloas(ctx, t, service)
	provider := service.(client.BuildersProvider)
	response, err := provider.Builders(ctx, &api.BuildersOpts{State: "finalized"})
	if err != nil {
		var apiErr *api.Error
		require.ErrorAs(t, err, &apiErr)
		require.Equal(t, nethttp.StatusNotFound, apiErr.StatusCode)
		t.Log("builder endpoint returned 404")
		return
	}
	require.NotNil(t, response)
	for index, builder := range response.Data {
		require.NotNil(t, builder.Builder)
		filtered, callErr := provider.Builders(ctx, &api.BuildersOpts{State: "finalized", Indices: []gloas.BuilderIndex{index}, Statuses: []apiv1.BuilderStatus{builder.Status}})
		require.NoError(t, callErr)
		require.Equal(t, builder, filtered.Data[index])
		byKey, callErr := provider.Builders(ctx, &api.BuildersOpts{State: "finalized", PubKeys: []phase0.BLSPubKey{builder.Builder.PublicKey}})
		require.NoError(t, callErr)
		require.Equal(t, builder, byKey.Data[index])
		return
	}
	t.Log("no registered builders available to test filters")
}
