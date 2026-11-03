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
	"strings"

	"github.com/attestantio/go-eth2-client/testclients"
	"io"
	nethttp "net/http"
	"net/http/httptest"
	"testing"

	client "github.com/attestantio/go-eth2-client"
	"github.com/attestantio/go-eth2-client/api"
	eth2http "github.com/attestantio/go-eth2-client/http"
	"github.com/stretchr/testify/require"
)

func TestBuildersTransport(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	type request struct{ method, path, contentType, body string }
	requests := make(chan request, 100)
	server := httptest.NewServer(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/eth/v1/node/version":
			_, _ = w.Write([]byte(`{"data":{"version":"test"}}`))
		case "/eth/v1/node/syncing":
			_, _ = w.Write([]byte(`{"data":{"is_syncing":false,"is_optimistic":false,"head_slot":"1","sync_distance":"0"}}`))
		default:
			body, _ := io.ReadAll(r.Body)
			requests <- request{r.Method, r.URL.Path, r.Header.Get("Content-Type"), string(body)}
			_, _ = w.Write([]byte(`{"execution_optimistic":false,"finalized":true,"data":[]}`))
		}
	}))
	defer server.Close()
	svc, err := eth2http.New(ctx, eth2http.WithAddress(server.URL))
	require.NoError(t, err)
	provider := svc.(client.BuildersProvider)
	pubkey := "0x" + strings.Repeat("11", 48)
	res, err := provider.Builders(ctx, &api.BuildersOpts{State: "head", IDs: []string{"3", pubkey}, Statuses: []string{"active"}})
	require.NoError(t, err)
	require.Empty(t, res.Data)
	got := <-requests
	require.Equal(t, nethttp.MethodPost, got.method)
	require.Equal(t, "/eth/v1/beacon/states/head/builders", got.path)
	require.Equal(t, "application/json", got.contentType)
	require.JSONEq(t, fmt.Sprintf(`{"ids":["3",%q],"statuses":["active"]}`, pubkey), got.body)

	res, err = provider.Builders(ctx, &api.BuildersOpts{State: "head"})
	require.NoError(t, err)
	require.Empty(t, res.Data)
	got = <-requests
	require.JSONEq(t, `{}`, got.body)
	require.Equal(t, true, res.Metadata["finalized"])
}

func TestBuildersErrors(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	server := httptest.NewServer(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
		switch r.URL.Path {
		case "/eth/v1/node/version":
			_, _ = w.Write([]byte(`{"data":{"version":"test"}}`))
		case "/eth/v1/node/syncing":
			_, _ = w.Write([]byte(`{"data":{"is_syncing":false,"is_optimistic":false,"head_slot":"1","sync_distance":"0"}}`))
		case "/eth/v1/beacon/states/broken/builders":
			_, _ = w.Write([]byte(`{"data":[`))
		case "/eth/v1/beacon/states/genesis/builders":
			nethttp.Error(w, "pre-Gloas", nethttp.StatusBadRequest)
		default:
			nethttp.NotFound(w, r)
		}
	}))
	defer server.Close()
	svc, err := eth2http.New(ctx, eth2http.WithAddress(server.URL))
	require.NoError(t, err)
	provider := svc.(client.BuildersProvider)
	_, err = provider.Builders(ctx, &api.BuildersOpts{State: "broken"})
	require.ErrorContains(t, err, "failed to parse JSON")
	_, err = provider.Builders(ctx, nil)
	require.ErrorIs(t, err, client.ErrNoOptions)
	_, err = provider.Builders(ctx, &api.BuildersOpts{})
	require.ErrorIs(t, err, client.ErrInvalidOptions)
	for _, test := range []struct {
		state  string
		status int
	}{{"genesis", 400}, {"head", 404}} {
		t.Run(test.state, func(t *testing.T) {
			result, err := provider.Builders(ctx, &api.BuildersOpts{State: test.state})
			require.Nil(t, result)
			var apiErr *api.Error
			require.True(t, errors.As(err, &apiErr))
			require.Equal(t, test.status, apiErr.StatusCode)
			require.Equal(t, nethttp.MethodPost, apiErr.Method)
			require.Equal(t, "/eth/v1/beacon/states/"+test.state+"/builders", apiErr.Endpoint)
		})
	}
}

func TestGloasBuildersLive(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	svc := testService(ctx, t).(client.Service)
	if !testclients.OnGloas(ctx, svc) {
		if requireGloas {
			t.Fatal("chain is not on Gloas")
		}
		t.Skip("chain is not on Gloas")
	}
	provider := svc.(client.BuildersProvider)
	result, err := provider.Builders(ctx, &api.BuildersOpts{State: "head", IDs: []string{"3"}})
	if err != nil {
		var apiErr *api.Error
		if errors.As(err, &apiErr) && apiErr.StatusCode == nethttp.StatusNotFound {
			if requireGloas {
				t.Fatal("node does not support the builders endpoint")
			}
			t.Skip("node does not support the builders endpoint")
		}
		require.NoError(t, err)
	}
	require.NotNil(t, result)
	require.Len(t, result.Data, 1)
	require.EqualValues(t, 3, result.Data[0].Index)
	require.NotNil(t, result.Data[0].Builder)
}

func TestBuildersInactive(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	server := httptest.NewServer(nethttp.HandlerFunc(func(nethttp.ResponseWriter, *nethttp.Request) {}))
	address := server.URL
	server.Close()
	svc, err := eth2http.New(ctx, eth2http.WithAddress(address), eth2http.WithAllowDelayedStart(true))
	require.NoError(t, err)
	_, err = svc.(client.BuildersProvider).Builders(ctx, &api.BuildersOpts{State: "head"})
	require.ErrorIs(t, err, client.ErrNotActive)
}
