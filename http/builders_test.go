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
	"strings"
	"testing"

	client "github.com/attestantio/go-eth2-client"
	"github.com/attestantio/go-eth2-client/api"
	"github.com/attestantio/go-eth2-client/http"
	"github.com/stretchr/testify/require"
)

func TestBuildersRequest(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var method, path, contentType, body string
	status := 200
	server := httptest.NewServer(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
		switch r.URL.Path {
		case "/eth/v1/node/version":
			_, _ = w.Write([]byte(`{"data":{"version":"test"}}`))
		case "/eth/v1/node/syncing":
			_, _ = w.Write([]byte(`{"data":{"is_syncing":false,"is_optimistic":false,"el_offline":false,"head_slot":"1","sync_distance":"0"}}`))
		default:
			method, path, contentType = r.Method, r.URL.Path, r.Header.Get("Content-Type")
			data, _ := io.ReadAll(r.Body)
			body = string(data)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(status)
			if status == 200 {
				_, _ = w.Write([]byte(`{"execution_optimistic":false,"finalized":true,"data":[]}`))
			} else {
				_, _ = w.Write([]byte(`{"code":400,"message":"error"}`))
			}
		}
	}))
	defer server.Close()
	service, err := http.New(ctx, http.WithAddress(server.URL))
	require.NoError(t, err)
	provider := service.(client.BuildersProvider)
	_, err = provider.Builders(ctx, nil)
	require.ErrorIs(t, err, client.ErrNoOptions)
	_, err = provider.Builders(ctx, &api.BuildersOpts{})
	require.ErrorIs(t, err, client.ErrInvalidOptions)
	key := "0x" + strings.Repeat("01", 48)
	response, err := provider.Builders(ctx, &api.BuildersOpts{State: "head", IDs: []string{"3", key}, Statuses: []string{"active"}})
	require.NoError(t, err)
	require.Empty(t, response.Data)
	require.Equal(t, false, response.Metadata["execution_optimistic"])
	require.Equal(t, "POST", method)
	require.Equal(t, "/eth/v1/beacon/states/head/builders", path)
	require.Equal(t, "application/json", contentType)
	require.JSONEq(t, `{"ids":["3","`+key+`"],"statuses":["active"]}`, body)
	_, err = provider.Builders(ctx, &api.BuildersOpts{State: "head"})
	require.NoError(t, err)
	require.JSONEq(t, `{}`, body)
	for _, code := range []int{400, 404} {
		status = code
		response, err = provider.Builders(ctx, &api.BuildersOpts{State: "genesis"})
		require.Nil(t, response)
		var apiErr *api.Error
		require.ErrorAs(t, err, &apiErr)
		require.Equal(t, code, apiErr.StatusCode)
		require.Equal(t, "/eth/v1/beacon/states/genesis/builders", apiErr.Endpoint)
	}
}

func TestBuildersGloas(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	service := testService(ctx, t).(client.Service)
	requireOnGloas(ctx, t, service)
	root, err := service.(client.BeaconStateRootProvider).BeaconStateRoot(ctx, &api.BeaconStateRootOpts{State: "finalized"})
	require.NoError(t, err)
	state := root.Data.String()
	provider := service.(client.BuildersProvider)
	response, err := provider.Builders(ctx, &api.BuildersOpts{State: state, IDs: []string{"3"}})
	require.NoError(t, err)
	require.Len(t, response.Data, 1)
	builder := response.Data[0]
	require.EqualValues(t, 3, builder.Index)
	filtered, err := provider.Builders(ctx, &api.BuildersOpts{State: state, IDs: []string{builder.Builder.PublicKey.String()}, Statuses: []string{builder.Status}})
	require.NoError(t, err)
	require.Equal(t, response.Data, filtered.Data)
}
