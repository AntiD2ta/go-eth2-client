// Copyright © 2026 Attestant Limited.
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package http_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
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

func TestGloasBuildersRequest(t *testing.T) {
	type request struct{ method, path, contentType, body string }
	fixture, err := os.ReadFile("../api/v1/testdata/builder-response.json")
	require.NoError(t, err)
	requests := make(chan request, 4)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/eth/v1/node/syncing":
			_, _ = w.Write([]byte(`{"data":{"head_slot":"0","sync_distance":"0","is_optimistic":false,"is_syncing":false}}`))
		case "/eth/v1/node/version":
			_, _ = w.Write([]byte(`{"data":{"version":"test"}}`))
		default:
			body, _ := io.ReadAll(r.Body)
			requests <- request{r.Method, r.URL.Path, r.Header.Get("Content-Type"), string(body)}
			switch r.URL.Path {
			case "/eth/v1/beacon/states/genesis/builders":
				http.Error(w, `{"code":400,"message":"pre-Gloas"}`, http.StatusBadRequest)
			case "/eth/v1/beacon/states/example/builders":
				_, _ = w.Write(append(append([]byte(`{"data":[`), fixture...), []byte(`]}`)...))
			case "/eth/v1/beacon/states/missing/builders":
				http.Error(w, `{"code":404,"message":"not found"}`, http.StatusNotFound)
			default:
				_, _ = w.Write([]byte(`{"execution_optimistic":false,"finalized":true,"data":[]}`))
			}
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
	response, err := provider.Builders(ctx, &api.BuildersOpts{State: "head"})
	require.NoError(t, err)
	require.Empty(t, response.Data)
	require.Equal(t, true, response.Metadata["finalized"])
	req := <-requests
	require.Equal(t, http.MethodPost, req.method)
	require.Equal(t, "/eth/v1/beacon/states/head/builders", req.path)
	require.Equal(t, "application/json", req.contentType)
	require.JSONEq(t, `{}`, req.body)

	key := phase0.BLSPubKey{1}
	_, err = provider.Builders(ctx, &api.BuildersOpts{State: "head", Indices: []gloas.BuilderIndex{3}, PubKeys: []phase0.BLSPubKey{key}, Statuses: []apiv1.BuilderStatus{apiv1.BuilderStatusActive}})
	require.NoError(t, err)
	req = <-requests
	require.Equal(t, http.MethodPost, req.method)
	require.Equal(t, "/eth/v1/beacon/states/head/builders", req.path)
	require.JSONEq(t, `{"ids":["3","`+key.String()+`"],"statuses":["active"]}`, req.body)

	example, err := provider.Builders(ctx, &api.BuildersOpts{State: "example"})
	require.NoError(t, err)
	require.Len(t, example.Data, 1)
	require.EqualValues(t, 1, example.Data[0].Index)
	require.Equal(t, apiv1.BuilderStatusActive, example.Data[0].Status)
	require.EqualValues(t, 1, example.Data[0].Builder.Balance)
	<-requests

	for _, tc := range []struct {
		state  string
		status int
	}{{"genesis", 400}, {"missing", 404}} {
		response, err := provider.Builders(ctx, &api.BuildersOpts{State: tc.state})
		require.Nil(t, response)
		var apiErr *api.Error
		require.True(t, errors.As(err, &apiErr), "%v", err)
		require.Equal(t, tc.status, apiErr.StatusCode)
		require.Equal(t, http.MethodPost, apiErr.Method)
		require.Equal(t, "/eth/v1/beacon/states/"+tc.state+"/builders", apiErr.Endpoint)
		req = <-requests
		require.Equal(t, http.MethodPost, req.method)
		require.Equal(t, "/eth/v1/beacon/states/"+tc.state+"/builders", req.path)
		require.JSONEq(t, `{}`, req.body)
	}
}

func TestGloasBuildersLive(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	service := testService(ctx, t).(client.Service)
	requireOnGloas(ctx, t, service)
	provider := service.(client.BuildersProvider)
	all, err := provider.Builders(ctx, &api.BuildersOpts{State: "finalized"})
	if err != nil {
		version, versionErr := service.(client.NodeVersionProvider).NodeVersion(ctx, &api.NodeVersionOpts{})
		require.NoError(t, versionErr)
		require.True(t, strings.Contains(strings.ToLower(version.Data), "nimbus"), "unexpected error from %s: %v", version.Data, err)
		var apiErr *api.Error
		require.True(t, errors.As(err, &apiErr), "%v", err)
		require.Equal(t, http.StatusNotFound, apiErr.StatusCode)
		require.Equal(t, http.MethodPost, apiErr.Method)
		require.Equal(t, "/eth/v1/beacon/states/finalized/builders", apiErr.Endpoint)
		require.Nil(t, all)
		t.Log("Nimbus does not expose the builders route; verified the 404 error path")
		return
	}
	require.NoError(t, err)
	require.NotNil(t, all)
	if len(all.Data) == 0 {
		return
	}
	builder := all.Data[0]
	require.NotNil(t, builder.Builder)
	filtered, err := provider.Builders(ctx, &api.BuildersOpts{State: "finalized", Indices: []gloas.BuilderIndex{builder.Index}, Statuses: []apiv1.BuilderStatus{builder.Status}})
	require.NoError(t, err)
	require.Equal(t, []*apiv1.Builder{builder}, filtered.Data)
	byKey, err := provider.Builders(ctx, &api.BuildersOpts{State: "finalized", PubKeys: []phase0.BLSPubKey{builder.Builder.PublicKey}})
	require.NoError(t, err)
	require.Equal(t, []*apiv1.Builder{builder}, byKey.Data)
}
