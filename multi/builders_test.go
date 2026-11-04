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
	"github.com/attestantio/go-eth2-client/spec/gloas"
	"github.com/stretchr/testify/require"
)

func TestBuilders(t *testing.T) {
	ctx := context.Background()
	m, err := mock.New(ctx)
	require.NoError(t, err)
	opts := &api.BuildersOpts{State: "head", Indices: []gloas.BuilderIndex{3}, Statuses: []string{"active"}}
	expected := &api.Response[[]*apiv1.Builder]{Data: []*apiv1.Builder{{Index: 3, Status: "active"}}}
	empty, err := m.Builders(ctx, opts)
	require.NoError(t, err)
	require.Empty(t, empty.Data)
	m.BuildersFunc = func(_ context.Context, got *api.BuildersOpts) (*api.Response[[]*apiv1.Builder], error) {
		require.Equal(t, opts, got)
		return expected, nil
	}
	result, err := m.Builders(ctx, opts)
	require.NoError(t, err)
	require.Equal(t, expected, result)
	combined, err := multi.New(ctx, multi.WithClients([]client.Service{m}))
	require.NoError(t, err)
	result, err = combined.(client.BuildersProvider).Builders(ctx, opts)
	require.NoError(t, err)
	require.Equal(t, expected, result)
}

func TestBuildersError(t *testing.T) {
	ctx := context.Background()
	m, err := mock.New(ctx)
	require.NoError(t, err)
	expected := &api.Error{StatusCode: 404, Method: "POST", Endpoint: "/eth/v1/beacon/states/head/builders"}
	m.BuildersFunc = func(context.Context, *api.BuildersOpts) (*api.Response[[]*apiv1.Builder], error) {
		return nil, expected
	}
	combined, err := multi.New(ctx, multi.WithClients([]client.Service{m}))
	require.NoError(t, err)
	response, err := combined.(client.BuildersProvider).Builders(ctx, &api.BuildersOpts{State: "head"})
	require.Nil(t, response)
	var actual *api.Error
	require.ErrorAs(t, err, &actual)
	require.Equal(t, expected, actual)
}
