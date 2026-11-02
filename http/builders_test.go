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
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	client "github.com/attestantio/go-eth2-client"
	"github.com/attestantio/go-eth2-client/api"
	apiv1 "github.com/attestantio/go-eth2-client/api/v1"
	eth2http "github.com/attestantio/go-eth2-client/http"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/require"
)

type builderRequest struct{ method, path, contentType, body string }

func TestBuildersRequest(t *testing.T) {
	requests := make(chan builderRequest, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/eth/v1/node/syncing":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":{"head_slot":"0","sync_distance":"0","is_optimistic":false,"is_syncing":false}}`))
		case "/eth/v1/node/version":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":{"version":"test"}}`))
		default:
			body, _ := io.ReadAll(r.Body)
			requests <- builderRequest{r.Method, r.URL.Path, r.Header.Get("Content-Type"), string(body)}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"execution_optimistic":false,"finalized":true,"data":[]}`))
		}
	}))
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	service, err := eth2http.New(ctx, eth2http.WithAddress(server.URL), eth2http.WithLogLevel(zerolog.Disabled))
	require.NoError(t, err)
	pubkey := "0x" + strings.Repeat("12", 48)
	result, err := service.(client.BuildersProvider).Builders(ctx, &api.BuildersOpts{State: "head", IDs: []string{"3", pubkey}, Statuses: []apiv1.BuilderStatus{apiv1.BuilderStatusActive}})
	require.NoError(t, err)
	require.Empty(t, result.Data)
	request := <-requests
	require.Equal(t, http.MethodPost, request.method)
	require.Equal(t, "/eth/v1/beacon/states/head/builders", request.path)
	require.Equal(t, "application/json", request.contentType)
	require.JSONEq(t, fmt.Sprintf(`{"ids":["3",%q],"statuses":["active"]}`, pubkey), request.body)
	_, err = service.(client.BuildersProvider).Builders(ctx, nil)
	require.True(t, errors.Is(err, client.ErrNoOptions))
	_, err = service.(client.BuildersProvider).Builders(ctx, &api.BuildersOpts{})
	require.True(t, errors.Is(err, client.ErrInvalidOptions))
}

func TestBuildersEmptyFilterAndErrors(t *testing.T) {
	for _, code := range []int{http.StatusOK, http.StatusBadRequest, http.StatusNotFound} {
		t.Run(http.StatusText(code), func(t *testing.T) {
			requests := make(chan builderRequest, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/eth/v1/node/syncing":
					w.Header().Set("Content-Type", "application/json")
					_, _ = w.Write([]byte(`{"data":{"head_slot":"0","sync_distance":"0","is_optimistic":false,"is_syncing":false}}`))
				case "/eth/v1/node/version":
					w.Header().Set("Content-Type", "application/json")
					_, _ = w.Write([]byte(`{"data":{"version":"test"}}`))
				default:
					b, _ := io.ReadAll(r.Body)
					requests <- builderRequest{r.Method, r.URL.Path, r.Header.Get("Content-Type"), string(b)}
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(code)
					if code == http.StatusOK {
						_, _ = w.Write([]byte(`{"data":[]}`))
					} else {
						_, _ = w.Write([]byte(`{"code":400,"message":"pre-Gloas state"}`))
					}
				}
			}))
			defer server.Close()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			service, err := eth2http.New(ctx, eth2http.WithAddress(server.URL), eth2http.WithLogLevel(zerolog.Disabled))
			require.NoError(t, err)
			result, err := service.(client.BuildersProvider).Builders(ctx, &api.BuildersOpts{State: "genesis"})
			if code == http.StatusOK {
				require.NoError(t, err)
				require.Empty(t, result.Data)
			} else {
				require.Nil(t, result)
				var apiErr *api.Error
				require.ErrorAs(t, err, &apiErr)
				require.Equal(t, code, apiErr.StatusCode)
				require.Equal(t, http.MethodPost, apiErr.Method)
				require.Equal(t, "/eth/v1/beacon/states/genesis/builders", apiErr.Endpoint)
			}
			req := <-requests
			require.Equal(t, http.MethodPost, req.method)
			require.Equal(t, "/eth/v1/beacon/states/genesis/builders", req.path)
			require.JSONEq(t, `{}`, req.body)
		})
	}
}

// TestBuildersGloas checks both enumeration and filtering on a live node.
func TestBuildersGloas(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	service := testService(ctx, t).(client.Service)
	requireOnGloas(ctx, t, service)
	provider := service.(client.BuildersProvider)
	response, err := provider.Builders(ctx, &api.BuildersOpts{State: "head"})
	if err != nil {
		var apiErr *api.Error
		require.ErrorAs(t, err, &apiErr)
		require.Equal(t, http.StatusNotFound, apiErr.StatusCode)
		require.Equal(t, "/eth/v1/beacon/states/head/builders", apiErr.Endpoint)
		t.Log("node does not expose builders for head")
		return
	}
	require.NotNil(t, response)
	require.NotEmpty(t, response.Data)
	first := response.Data[0]
	require.NotNil(t, first)
	require.NotNil(t, first.Builder)
	filtered, err := provider.Builders(ctx, &api.BuildersOpts{
		State: "head", IDs: []string{fmt.Sprint(first.Index)}, Statuses: []apiv1.BuilderStatus{first.Status},
	})
	require.NoError(t, err)
	require.Len(t, filtered.Data, 1)
	require.Equal(t, first.Index, filtered.Data[0].Index)
	require.Equal(t, first.Status, filtered.Data[0].Status)
	require.Equal(t, first.Builder.PublicKey, filtered.Data[0].Builder.PublicKey)
	pubkeyFiltered, err := provider.Builders(ctx, &api.BuildersOpts{State: "head", IDs: []string{first.Builder.PublicKey.String()}})
	require.NoError(t, err)
	require.Len(t, pubkeyFiltered.Data, 1)
	require.Equal(t, first.Index, pubkeyFiltered.Data[0].Index)
	statusFiltered, err := provider.Builders(ctx, &api.BuildersOpts{State: "head", Statuses: []apiv1.BuilderStatus{first.Status}})
	require.NoError(t, err)
	require.NotEmpty(t, statusFiltered.Data)
	for _, builder := range statusFiltered.Data {
		require.Equal(t, first.Status, builder.Status)
	}
}
