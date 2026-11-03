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
	"strings"
	"testing"

	client "github.com/attestantio/go-eth2-client"
	"github.com/attestantio/go-eth2-client/api"
	clienthttp "github.com/attestantio/go-eth2-client/http"
	"github.com/attestantio/go-eth2-client/spec/gloas"
	"github.com/attestantio/go-eth2-client/spec/phase0"
	"github.com/stretchr/testify/require"
)

func TestBuildersPostsObject(t *testing.T) {
	type request struct {
		method, path, contentType string
		body                      []byte
	}
	requests := make(chan request, 1)
	server := httptest.NewServer(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/eth/v1/node/version":
			fmt.Fprint(w, `{"data":{"version":"stub"}}`)
		case "/eth/v1/node/syncing":
			fmt.Fprint(w, `{"data":{"head_slot":"1","sync_distance":"0","is_syncing":false,"is_optimistic":false,"el_offline":false}}`)
		default:
			body, _ := io.ReadAll(r.Body)
			requests <- request{r.Method, r.URL.Path, r.Header.Get("Content-Type"), body}
			fmt.Fprint(w, `{"execution_optimistic":false,"finalized":true,"data":[]}`)
		}
	}))
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	svc, err := clienthttp.New(ctx, clienthttp.WithAddress(server.URL))
	require.NoError(t, err)
	key := phase0.BLSPubKey{1}
	result, err := svc.(client.BuildersProvider).Builders(ctx, &api.BuildersOpts{State: "head", Indices: []gloas.BuilderIndex{3}, PubKeys: []phase0.BLSPubKey{key}, Statuses: []string{"active"}})
	require.NoError(t, err)
	require.Empty(t, result.Data)
	require.Equal(t, true, result.Metadata["finalized"])
	req := <-requests
	require.Equal(t, "POST", req.method)
	require.Equal(t, "/eth/v1/beacon/states/head/builders", req.path)
	require.Equal(t, "application/json", req.contentType)
	var body map[string][]string
	require.NoError(t, json.Unmarshal(req.body, &body))
	require.Equal(t, map[string][]string{"ids": {"3", key.String()}, "statuses": {"active"}}, body)

	_, err = svc.(client.BuildersProvider).Builders(ctx, &api.BuildersOpts{State: "head", Statuses: []string{"active"}})
	require.NoError(t, err)
	req = <-requests
	require.JSONEq(t, `{"statuses":["active"]}`, string(req.body))
}

func TestBuildersUnfilteredAndErrors(t *testing.T) {
	for _, test := range []struct {
		name   string
		status int
	}{
		{name: "Unfiltered", status: 200},
		{name: "PreGloas", status: 400},
		{name: "NotFound", status: 404},
	} {
		t.Run(test.name, func(t *testing.T) {
			body := make(chan []byte, 1)
			server := httptest.NewServer(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/eth/v1/node/version":
					fmt.Fprint(w, `{"data":{"version":"stub"}}`)
				case "/eth/v1/node/syncing":
					fmt.Fprint(w, `{"data":{"head_slot":"1","sync_distance":"0","is_syncing":false,"is_optimistic":false,"el_offline":false}}`)
				case "/eth/v1/beacon/states/genesis/builders":
					data, _ := io.ReadAll(r.Body)
					body <- data
					w.WriteHeader(test.status)
					if test.status == 200 {
						fmt.Fprint(w, `{"execution_optimistic":false,"finalized":true,"data":[]}`)
					} else {
						fmt.Fprintf(w, `{"code":%d,"message":"error"}`, test.status)
					}
				default:
					w.WriteHeader(nethttp.StatusNotFound)
				}
			}))
			defer server.Close()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			svc, err := clienthttp.New(ctx, clienthttp.WithAddress(server.URL))
			require.NoError(t, err)
			result, err := svc.(client.BuildersProvider).Builders(ctx, &api.BuildersOpts{State: "genesis"})
			require.JSONEq(t, `{}`, string(<-body))
			if test.status == 200 {
				require.NoError(t, err)
				require.NotNil(t, result)
				require.Empty(t, result.Data)
			} else {
				require.Nil(t, result)
				var apiErr *api.Error
				require.ErrorAs(t, err, &apiErr)
				require.Equal(t, test.status, apiErr.StatusCode)
				require.Equal(t, "POST", apiErr.Method)
				require.Equal(t, "/eth/v1/beacon/states/genesis/builders", apiErr.Endpoint)
			}
		})
	}
}

func TestBuildersGloasLive(t *testing.T) {
	ctx := context.Background()
	service := testService(ctx, t)
	requireOnGloas(ctx, t, service)
	provider := service.(client.BuildersProvider)
	all, err := provider.Builders(ctx, &api.BuildersOpts{State: "finalized"})
	if err != nil {
		version, versionErr := service.(client.NodeVersionProvider).NodeVersion(ctx, &api.NodeVersionOpts{})
		require.NoError(t, versionErr)
		require.Contains(t, strings.ToLower(version.Data), "nimbus")
		var apiErr *api.Error
		require.ErrorAs(t, err, &apiErr)
		require.Equal(t, nethttp.StatusNotFound, apiErr.StatusCode)
		require.Equal(t, "/eth/v1/beacon/states/finalized/builders", apiErr.Endpoint)
		t.Log("Nimbus returned 404 for the builders endpoint")
		return
	}
	require.NotEmpty(t, all.Data)
	first := all.Data[0]
	require.NotNil(t, first.Builder)
	filtered, err := provider.Builders(ctx, &api.BuildersOpts{State: "finalized", Indices: []gloas.BuilderIndex{first.Index}, Statuses: []string{first.Status}})
	require.NoError(t, err)
	require.Len(t, filtered.Data, 1)
	require.Equal(t, first, filtered.Data[0])
	byKey, err := provider.Builders(ctx, &api.BuildersOpts{State: "finalized", PubKeys: []phase0.BLSPubKey{first.Builder.PublicKey}})
	require.NoError(t, err)
	require.Len(t, byKey.Data, 1)
	require.Equal(t, first, byKey.Data[0])
}
