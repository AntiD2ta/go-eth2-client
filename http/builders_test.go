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

	client "github.com/attestantio/go-eth2-client"
	"github.com/attestantio/go-eth2-client/api"
	apiv1 "github.com/attestantio/go-eth2-client/api/v1"
	eth2http "github.com/attestantio/go-eth2-client/http"
	"github.com/attestantio/go-eth2-client/spec/gloas"
	"github.com/attestantio/go-eth2-client/spec/phase0"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/require"
)

func TestBuildersRequest(t *testing.T) {
	type request struct{ method, path, contentType, body string }
	requests := make(chan request, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/eth/v1/node/syncing":
			_, _ = io.WriteString(w, `{"data":{"head_slot":"0","sync_distance":"0","is_syncing":false,"is_optimistic":false}}`)
		case "/eth/v1/node/version":
			_, _ = io.WriteString(w, `{"data":{"version":"test"}}`)
		default:
			body, _ := io.ReadAll(r.Body)
			requests <- request{r.Method, r.URL.Path, r.Header.Get("Content-Type"), string(body)}
			_, _ = io.WriteString(w, `{"execution_optimistic":false,"finalized":true,"data":[]}`)
		}
	}))
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	service, err := eth2http.New(ctx, eth2http.WithAddress(server.URL), eth2http.WithLogLevel(zerolog.Disabled))
	require.NoError(t, err)
	provider := service.(client.BuildersProvider)
	key := phase0.BLSPubKey{1, 2}
	for _, tc := range []struct {
		opts *api.BuildersOpts
		body string
	}{
		{&api.BuildersOpts{State: "head"}, `{}`},
		{&api.BuildersOpts{State: "head", Indices: []gloas.BuilderIndex{3}, PubKeys: []phase0.BLSPubKey{key}, Statuses: []string{"active"}}, `{"ids":["3","` + key.String() + `"],"statuses":["active"]}`},
	} {
		response, err := provider.Builders(ctx, tc.opts)
		require.NoError(t, err)
		require.Empty(t, response.Data)
		require.Equal(t, true, response.Metadata["finalized"])
		got := <-requests
		require.Equal(t, http.MethodPost, got.method)
		require.Equal(t, "/eth/v1/beacon/states/head/builders", got.path)
		require.Equal(t, "application/json", got.contentType)
		require.JSONEq(t, tc.body, got.body)
	}
}

func TestBuildersErrors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/eth/v1/node/syncing":
			_, _ = io.WriteString(w, `{"data":{"head_slot":"0","sync_distance":"0","is_syncing":false,"is_optimistic":false}}`)
		case "/eth/v1/node/version":
			_, _ = io.WriteString(w, `{"data":{"version":"test"}}`)
		case "/eth/v1/beacon/states/genesis/builders":
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, `{"code":400,"message":"pre-Gloas state"}`)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	service, err := eth2http.New(ctx, eth2http.WithAddress(server.URL), eth2http.WithLogLevel(zerolog.Disabled))
	require.NoError(t, err)
	provider := service.(client.BuildersProvider)
	_, err = provider.Builders(ctx, nil)
	require.ErrorIs(t, err, client.ErrNoOptions)
	_, err = provider.Builders(ctx, &api.BuildersOpts{})
	require.ErrorIs(t, err, client.ErrInvalidOptions)
	for _, tc := range []struct {
		state  string
		status int
	}{{"genesis", 400}, {"head", 404}} {
		response, err := provider.Builders(ctx, &api.BuildersOpts{State: tc.state})
		require.Nil(t, response)
		var apiErr *api.Error
		require.ErrorAs(t, err, &apiErr)
		require.Equal(t, tc.status, apiErr.StatusCode)
		require.Equal(t, http.MethodPost, apiErr.Method)
		require.Equal(t, "/eth/v1/beacon/states/"+tc.state+"/builders", apiErr.Endpoint)
	}
}

func TestGloasBuilders(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	service := testService(ctx, t).(client.Service)
	requireOnGloas(ctx, t, service)
	provider := service.(client.BuildersProvider)
	all, err := provider.Builders(ctx, &api.BuildersOpts{State: "finalized"})
	require.NoError(t, err)
	require.NotEmpty(t, all.Data)
	var selected *apiv1.Builder
	for _, builder := range all.Data {
		selected = builder
		break
	}
	filtered, err := provider.Builders(ctx, &api.BuildersOpts{State: "finalized", Indices: []gloas.BuilderIndex{selected.Index}})
	require.NoError(t, err)
	require.Len(t, filtered.Data, 1)
	require.Equal(t, selected, filtered.Data[selected.Index])
	byKey, err := provider.Builders(ctx, &api.BuildersOpts{State: "finalized", PubKeys: []phase0.BLSPubKey{selected.Builder.PublicKey}, Statuses: []string{selected.Status}})
	require.NoError(t, err)
	require.Equal(t, selected, byKey.Data[selected.Index])
}
