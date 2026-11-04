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
	"testing"

	client "github.com/attestantio/go-eth2-client"
	"github.com/attestantio/go-eth2-client/api"
	"github.com/attestantio/go-eth2-client/http"
	"github.com/attestantio/go-eth2-client/spec/gloas"
	"github.com/attestantio/go-eth2-client/spec/phase0"
	"github.com/stretchr/testify/require"
)

func TestBuildersRequest(t *testing.T) {
	ctx := context.Background()
	type request struct{ method, path, body string }
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
			requests <- request{r.Method, r.URL.Path, string(body)}
			fmt.Fprint(w, `{"execution_optimistic":false,"finalized":false,"data":[]}`)
		}
	}))
	defer server.Close()
	service, err := http.New(ctx, http.WithAddress(server.URL))
	require.NoError(t, err)
	key := phase0.BLSPubKey{1}
	res, err := service.(client.BuildersProvider).Builders(ctx, &api.BuildersOpts{State: "head", Indices: []gloas.BuilderIndex{3}, PubKeys: []phase0.BLSPubKey{key}, Statuses: []string{"active"}})
	require.NoError(t, err)
	require.NotNil(t, res)
	select {
	case got := <-requests:
		require.Equal(t, "POST", got.method)
		require.Equal(t, "/eth/v1/beacon/states/head/builders", got.path)
		require.JSONEq(t, fmt.Sprintf(`{"ids":["3",%q],"statuses":["active"]}`, key.String()), got.body)
	default:
		t.Fatal("builder request not sent")
	}
}

func TestBuildersErrorsAndUnfilteredBody(t *testing.T) {
	ctx := context.Background()
	for _, status := range []int{200, 400, 404} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			requests := make(chan string, 1)
			server := httptest.NewServer(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/eth/v1/node/version":
					fmt.Fprint(w, `{"data":{"version":"stub"}}`)
				case "/eth/v1/node/syncing":
					fmt.Fprint(w, `{"data":{"head_slot":"1","sync_distance":"0","is_syncing":false,"is_optimistic":false,"el_offline":false}}`)
				default:
					body, _ := io.ReadAll(r.Body)
					requests <- string(body)
					w.WriteHeader(status)
					if status == 200 {
						fmt.Fprint(w, `{"execution_optimistic":false,"finalized":true,"data":[]}`)
					} else {
						fmt.Fprint(w, `{"code":400,"message":"error"}`)
					}
				}
			}))
			defer server.Close()
			service, err := http.New(ctx, http.WithAddress(server.URL))
			require.NoError(t, err)
			provider := service.(client.BuildersProvider)
			state := "head"
			if status == 400 {
				state = "genesis"
			}
			res, err := provider.Builders(ctx, &api.BuildersOpts{State: state})
			require.JSONEq(t, `{}`, <-requests)
			if status == 200 {
				require.NoError(t, err)
				require.Empty(t, res.Data)
				require.Equal(t, true, res.Metadata["finalized"])
			} else {
				require.Nil(t, res)
				var apiErr *api.Error
				require.ErrorAs(t, err, &apiErr)
				require.Equal(t, status, apiErr.StatusCode)
				require.Equal(t, "POST", apiErr.Method)
				require.Equal(t, "/eth/v1/beacon/states/"+state+"/builders", apiErr.Endpoint)
			}
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
		require.Equal(t, "POST", apiErr.Method)
		t.Log("node does not provide the builders endpoint")
		return
	}
	require.NotNil(t, response)
	require.NotEmpty(t, response.Data)
	for _, builder := range response.Data {
		require.NotNil(t, builder)
		require.NotNil(t, builder.Builder)
		require.EqualValues(t, 3, builder.Index)
	}
}
