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
	"fmt"
	"io"
	nethttp "net/http"
	"net/http/httptest"
	"strings"
	"testing"

	client "github.com/attestantio/go-eth2-client"
	"github.com/attestantio/go-eth2-client/api"
	apiv1 "github.com/attestantio/go-eth2-client/api/v1"
	ethhttp "github.com/attestantio/go-eth2-client/http"
	"github.com/stretchr/testify/require"
)

func TestBuildersRequest(t *testing.T) {
	key := "0x" + strings.Repeat("01", 48)
	requests := make(chan string, 1)
	srv := httptest.NewServer(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
		switch r.URL.Path {
		case "/eth/v1/node/version":
			_, _ = w.Write([]byte(`{"data":{"version":"test"}}`))
		case "/eth/v1/node/syncing":
			_, _ = w.Write([]byte(`{"data":{"is_syncing":false,"is_optimistic":false,"el_offline":false,"head_slot":"1","sync_distance":"0"}}`))
		default:
			b, _ := io.ReadAll(r.Body)
			requests <- fmt.Sprintf("%s %s %s %s", r.Method, r.URL.Path, r.Header.Get("Content-Type"), b)
			_, _ = w.Write([]byte(`{"execution_optimistic":false,"finalized":true,"data":[]}`))
		}
	}))
	defer srv.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	service, err := ethhttp.New(ctx, ethhttp.WithAddress(srv.URL))
	require.NoError(t, err)
	result, err := service.(client.BuildersProvider).Builders(ctx, &api.BuildersOpts{State: "head", IDs: []string{"3", key}, Statuses: []apiv1.BuilderStatus{apiv1.BuilderStatusActive}})
	require.NoError(t, err)
	require.Empty(t, result.Data)
	got := <-requests
	require.Equal(t, fmt.Sprintf(`POST /eth/v1/beacon/states/head/builders application/json {"ids":["3","%s"],"statuses":["active"]}`, key), got)
}

func TestBuildersHTTPResponses(t *testing.T) {
	tests := []struct {
		name   string
		status int
		body   string
	}{
		{name: "Empty", status: 200, body: `{"execution_optimistic":false,"finalized":false,"data":[]}`},
		{name: "PreGloas", status: 400, body: `{"code":400,"message":"pre-Gloas state"}`},
		{name: "Unsupported", status: 404, body: `{"code":404,"message":"not found"}`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			requests := make(chan string, 1)
			srv := httptest.NewServer(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
				switch r.URL.Path {
				case "/eth/v1/node/version":
					_, _ = w.Write([]byte(`{"data":{"version":"test"}}`))
				case "/eth/v1/node/syncing":
					_, _ = w.Write([]byte(`{"data":{"is_syncing":false,"is_optimistic":false,"el_offline":false,"head_slot":"1","sync_distance":"0"}}`))
				default:
					body, _ := io.ReadAll(r.Body)
					requests <- fmt.Sprintf("%s %s %s", r.Method, r.URL.Path, body)
					w.WriteHeader(tc.status)
					_, _ = w.Write([]byte(tc.body))
				}
			}))
			defer srv.Close()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			svc, err := ethhttp.New(ctx, ethhttp.WithAddress(srv.URL))
			require.NoError(t, err)
			result, err := svc.(client.BuildersProvider).Builders(ctx, &api.BuildersOpts{State: "genesis"})
			require.Equal(t, "POST /eth/v1/beacon/states/genesis/builders {}", <-requests)
			if tc.status == 200 {
				require.NoError(t, err)
				require.Empty(t, result.Data)
			} else {
				require.Nil(t, result)
				var apiErr *api.Error
				require.ErrorAs(t, err, &apiErr)
				require.Equal(t, tc.status, apiErr.StatusCode)
				require.Equal(t, "POST", apiErr.Method)
				require.Equal(t, "/eth/v1/beacon/states/genesis/builders", apiErr.Endpoint)
			}
		})
	}
}

func TestGloasBuilders(t *testing.T) {
	ctx := context.Background()
	service := testService(ctx, t)
	requireOnGloas(ctx, t, service)
	res, err := service.(client.BuildersProvider).Builders(ctx, &api.BuildersOpts{State: "head", IDs: []string{"3"}})
	if err != nil {
		var apiErr *api.Error
		require.ErrorAs(t, err, &apiErr)
		require.Equal(t, nethttp.StatusNotFound, apiErr.StatusCode)
		require.Equal(t, "/eth/v1/beacon/states/head/builders", apiErr.Endpoint)
		t.Log("node does not provide the builders endpoint")
		return
	}
	require.NotNil(t, res)
	for _, builder := range res.Data {
		require.EqualValues(t, 3, builder.Index)
		require.NotNil(t, builder.Builder)
	}
}
