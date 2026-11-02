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

type buildersRequest struct{ method, path, contentType, body string }

func TestBuildersLocal(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	requests := make(chan buildersRequest, 3)
	server := httptest.NewServer(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
		switch r.URL.Path {
		case "/eth/v1/node/version":
			_, _ = w.Write([]byte(`{"data":{"version":"test"}}`))
		case "/eth/v1/node/syncing":
			_, _ = w.Write([]byte(`{"data":{"is_syncing":false,"is_optimistic":false,"el_offline":false,"head_slot":"1","sync_distance":"0"}}`))
		default:
			body, _ := io.ReadAll(r.Body)
			requests <- buildersRequest{r.Method, r.URL.Path, r.Header.Get("Content-Type"), string(body)}
			w.Header().Set("Content-Type", "application/json")
			if r.URL.Path == "/eth/v1/beacon/states/genesis/builders" {
				w.WriteHeader(nethttp.StatusBadRequest)
				return
			}
			if r.URL.Path == "/eth/v1/beacon/states/missing/builders" {
				w.WriteHeader(nethttp.StatusNotFound)
				return
			}
			_, _ = w.Write([]byte(`{"execution_optimistic":false,"finalized":true,"data":[]}`))
		}
	}))
	defer server.Close()
	service, err := http.New(ctx, http.WithAddress(server.URL))
	require.NoError(t, err)
	provider := service.(client.BuildersProvider)
	key := phase0.BLSPubKey{1}
	for _, tc := range []struct {
		name string
		opts *api.BuildersOpts
		body string
	}{
		{"all", &api.BuildersOpts{State: "head"}, `{}`},
		{"filters", &api.BuildersOpts{State: "finalized", Indices: []gloas.BuilderIndex{3}, PubKeys: []phase0.BLSPubKey{key}, Statuses: []apiv1.BuilderStatus{apiv1.BuilderStatusActive}}, `{"ids":["3","` + key.String() + `"],"statuses":["active"]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res, err := provider.Builders(ctx, tc.opts)
			require.NoError(t, err)
			require.Empty(t, res.Data)
			require.Equal(t, true, res.Metadata["finalized"])
			req := <-requests
			require.Equal(t, nethttp.MethodPost, req.method)
			require.Equal(t, "/eth/v1/beacon/states/"+tc.opts.State+"/builders", req.path)
			require.Equal(t, "application/json", req.contentType)
			require.JSONEq(t, tc.body, req.body)
		})
	}
}

func TestBuildersErrors(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	server := httptest.NewServer(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
		switch r.URL.Path {
		case "/eth/v1/node/version":
			_, _ = w.Write([]byte(`{"data":{"version":"test"}}`))
		case "/eth/v1/node/syncing":
			_, _ = w.Write([]byte(`{"data":{"is_syncing":false,"is_optimistic":false,"el_offline":false,"head_slot":"1","sync_distance":"0"}}`))
		case "/eth/v1/beacon/states/genesis/builders":
			w.WriteHeader(nethttp.StatusBadRequest)
		default:
			w.WriteHeader(nethttp.StatusNotFound)
		}
	}))
	defer server.Close()
	service, err := http.New(ctx, http.WithAddress(server.URL))
	require.NoError(t, err)
	p := service.(client.BuildersProvider)
	_, err = p.Builders(ctx, nil)
	require.ErrorIs(t, err, client.ErrNoOptions)
	_, err = p.Builders(ctx, &api.BuildersOpts{})
	require.ErrorIs(t, err, client.ErrInvalidOptions)
	for _, tc := range []struct {
		state  string
		status int
	}{{"genesis", 400}, {"missing", 404}} {
		t.Run(tc.state, func(t *testing.T) {
			res, err := p.Builders(ctx, &api.BuildersOpts{State: tc.state})
			require.Nil(t, res)
			var apiErr *api.Error
			require.ErrorAs(t, err, &apiErr)
			require.Equal(t, tc.status, apiErr.StatusCode)
			require.Equal(t, nethttp.MethodPost, apiErr.Method)
			require.Contains(t, apiErr.Endpoint, "/eth/v1/beacon/states/"+tc.state+"/builders")
		})
	}
}

func TestGloasBuilders(t *testing.T) {
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
		require.Contains(t, apiErr.Endpoint, "/eth/v1/beacon/states/finalized/builders")
		return
	}
	if len(response.Data) == 0 {
		filtered, err := provider.Builders(ctx, &api.BuildersOpts{State: "finalized", Indices: []gloas.BuilderIndex{0}})
		require.NoError(t, err)
		require.Empty(t, filtered.Data)
		return
	}
	builder := response.Data[0]
	require.NotNil(t, builder.Builder)
	byIndex, err := provider.Builders(ctx, &api.BuildersOpts{State: "finalized", Indices: []gloas.BuilderIndex{builder.Index}})
	require.NoError(t, err)
	require.Equal(t, []*apiv1.Builder{builder}, byIndex.Data)
	byKey, err := provider.Builders(ctx, &api.BuildersOpts{State: "finalized", PubKeys: []phase0.BLSPubKey{builder.Builder.PublicKey}, Statuses: []apiv1.BuilderStatus{builder.Status}})
	require.NoError(t, err)
	require.Equal(t, []*apiv1.Builder{builder}, byKey.Data)
}
