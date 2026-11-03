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

package multi_test

import (
	"context"
	"testing"

	client "github.com/attestantio/go-eth2-client"
	"github.com/attestantio/go-eth2-client/api"
	apiv1 "github.com/attestantio/go-eth2-client/api/v1"
	"github.com/attestantio/go-eth2-client/mock"
	"github.com/attestantio/go-eth2-client/multi"
	"github.com/stretchr/testify/require"
)

func TestBuildersForwarded(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	backend, err := mock.New(ctx, mock.WithName("builder"))
	require.NoError(t, err)
	opts := &api.BuildersOpts{State: "head", IDs: []string{"3"}, Statuses: []string{"active"}}
	expected := &api.Response[[]*apiv1.Builder]{Data: []*apiv1.Builder{{Index: 3, Status: "active"}}}
	backend.BuildersFunc = func(_ context.Context, actual *api.BuildersOpts) (*api.Response[[]*apiv1.Builder], error) {
		require.Equal(t, opts, actual)
		return expected, nil
	}
	result, err := backend.Builders(ctx, opts)
	require.NoError(t, err)
	require.Equal(t, expected, result)
	aggregate, err := multi.New(ctx, multi.WithClients([]client.Service{backend}))
	require.NoError(t, err)
	result, err = aggregate.(client.BuildersProvider).Builders(ctx, opts)
	require.NoError(t, err)
	require.Equal(t, expected, result)
}

func TestBuildersErrorsAndDefault(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	backend, err := mock.New(ctx, mock.WithName("builder"))
	require.NoError(t, err)
	response, err := backend.Builders(ctx, &api.BuildersOpts{State: "head"})
	require.NoError(t, err)
	require.NotNil(t, response)
	require.Empty(t, response.Data)
	backend.BuildersFunc = func(context.Context, *api.BuildersOpts) (*api.Response[[]*apiv1.Builder], error) {
		return nil, &api.Error{StatusCode: 404, Method: "POST", Endpoint: "/eth/v1/beacon/states/head/builders"}
	}
	aggregate, err := multi.New(ctx, multi.WithClients([]client.Service{backend}))
	require.NoError(t, err)
	response, err = aggregate.(client.BuildersProvider).Builders(ctx, &api.BuildersOpts{State: "head"})
	require.Nil(t, response)
	var apiErr *api.Error
	require.ErrorAs(t, err, &apiErr)
	require.Equal(t, 404, apiErr.StatusCode)
}
