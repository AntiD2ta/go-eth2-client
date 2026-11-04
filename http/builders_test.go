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
	nethttp "net/http"
	"net/http/httptest"
	"strings"
	"testing"

	client "github.com/attestantio/go-eth2-client"
	"github.com/attestantio/go-eth2-client/api"
	eth2http "github.com/attestantio/go-eth2-client/http"
	"github.com/stretchr/testify/require"
)

func TestBuildersTransport(t *testing.T) {
	ctx := context.Background()
	requests := make(chan string, 1)
	server := httptest.NewServer(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/eth/v1/node/version":
			fmt.Fprint(w, `{"data":{"version":"stub"}}`)
		case "/eth/v1/node/syncing":
			fmt.Fprint(w, `{"data":{"head_slot":"1","sync_distance":"0","is_syncing":false,"is_optimistic":false,"el_offline":false}}`)
		case "/eth/v1/beacon/states/head/builders":
			data, _ := io.ReadAll(r.Body)
			requests <- r.Method + " " + r.URL.Path + " " + string(data)
			fmt.Fprint(w, `{"execution_optimistic":false,"finalized":true,"data":[]}`)
		case "/eth/v1/beacon/states/genesis/builders":
			w.WriteHeader(nethttp.StatusBadRequest)
			fmt.Fprint(w, `{"code":400,"message":"pre-Gloas state"}`)
		default:
			nethttp.NotFound(w, r)
		}
	}))
	defer server.Close()
	service, err := eth2http.New(ctx, eth2http.WithAddress(server.URL))
	require.NoError(t, err)
	response, err := service.(client.BuildersProvider).Builders(ctx, &api.BuildersOpts{State: "head"})
	require.NoError(t, err)
	require.Empty(t, response.Data)
	require.Equal(t, true, response.Metadata["finalized"])
	require.Equal(t, "POST /eth/v1/beacon/states/head/builders {}", <-requests)
	key := "0x" + strings.Repeat("ab", 48)
	_, err = service.(client.BuildersProvider).Builders(ctx, &api.BuildersOpts{State: "head", IDs: []string{"3", key}, Statuses: []string{"active", "pending"}})
	require.NoError(t, err)
	request := <-requests
	require.Equal(t, "POST /eth/v1/beacon/states/head/builders ", request[:len("POST /eth/v1/beacon/states/head/builders ")])
	require.JSONEq(t, fmt.Sprintf(`{"ids":["3",%q],"statuses":["active","pending"]}`, key), strings.TrimPrefix(request, "POST /eth/v1/beacon/states/head/builders "))
	for _, test := range []struct {
		state  string
		status int
	}{{"genesis", 400}, {"missing", 404}} {
		_, err = service.(client.BuildersProvider).Builders(ctx, &api.BuildersOpts{State: test.state})
		var apiErr *api.Error
		require.True(t, errors.As(err, &apiErr))
		require.Equal(t, test.status, apiErr.StatusCode)
		require.Equal(t, "POST", apiErr.Method)
		require.Equal(t, "/eth/v1/beacon/states/"+test.state+"/builders", apiErr.Endpoint)
	}
	_, err = service.(client.BuildersProvider).Builders(ctx, nil)
	require.ErrorIs(t, err, client.ErrNoOptions)
	_, err = service.(client.BuildersProvider).Builders(ctx, &api.BuildersOpts{})
	require.ErrorIs(t, err, client.ErrInvalidOptions)
}

func TestBuildersGloas(t *testing.T) {
	ctx := context.Background()
	service := testService(ctx, t).(client.Service)
	requireOnGloas(ctx, t, service)
	provider := service.(client.BuildersProvider)
	response, err := provider.Builders(ctx, &api.BuildersOpts{State: "head", IDs: []string{"3"}})
	require.NoError(t, err)
	require.Len(t, response.Data, 1)
	builder := response.Data[0]
	require.EqualValues(t, 3, builder.Index)
	require.NotNil(t, builder.Builder)
	filtered, err := provider.Builders(ctx, &api.BuildersOpts{State: "head", IDs: []string{builder.Builder.PublicKey.String()}, Statuses: []string{builder.Status}})
	require.NoError(t, err)
	require.Len(t, filtered.Data, 1)
	require.Equal(t, builder.Index, filtered.Data[0].Index)
	require.Equal(t, builder.Builder.PublicKey, filtered.Data[0].Builder.PublicKey)
}
