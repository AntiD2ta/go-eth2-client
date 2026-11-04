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
	"time"

	client "github.com/attestantio/go-eth2-client"
	"github.com/attestantio/go-eth2-client/api"
	apiv1 "github.com/attestantio/go-eth2-client/api/v1"
	"github.com/attestantio/go-eth2-client/mock"
	"github.com/attestantio/go-eth2-client/multi"
	"github.com/attestantio/go-eth2-client/testclients"
	"github.com/stretchr/testify/require"
)

func TestBuildersForwarding(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	backend, err := mock.New(ctx)
	require.NoError(t, err)
	expected := &api.Response[[]*apiv1.Builder]{Data: []*apiv1.Builder{{Index: 3, Status: apiv1.BuilderStatusActive}}}
	opts := &api.BuildersOpts{State: "head", Statuses: []apiv1.BuilderStatus{apiv1.BuilderStatusActive}}
	backend.BuildersFunc = func(_ context.Context, got *api.BuildersOpts) (*api.Response[[]*apiv1.Builder], error) {
		require.Equal(t, opts, got)
		return expected, nil
	}
	direct, err := backend.Builders(ctx, opts)
	require.NoError(t, err)
	require.Same(t, expected, direct)
	service, err := multi.New(ctx, multi.WithClients([]client.Service{backend}))
	require.NoError(t, err)
	result, err := service.(client.BuildersProvider).Builders(ctx, opts)
	require.NoError(t, err)
	require.Same(t, expected, result)
}

func TestBuildersTestClients(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	backend, err := mock.New(ctx)
	require.NoError(t, err)
	opts := &api.BuildersOpts{State: "head"}
	expected := &api.Response[[]*apiv1.Builder]{Data: []*apiv1.Builder{{Index: 3}}}
	backend.BuildersFunc = func(_ context.Context, got *api.BuildersOpts) (*api.Response[[]*apiv1.Builder], error) {
		require.Equal(t, opts, got)
		return expected, nil
	}
	erroring, err := testclients.NewErroring(ctx, 0, backend)
	require.NoError(t, err)
	sleepy, err := testclients.NewSleepy(ctx, 0, time.Millisecond, backend)
	require.NoError(t, err)
	for _, wrapper := range []client.Service{erroring, sleepy} {
		provider, ok := wrapper.(client.BuildersProvider)
		require.True(t, ok)
		response, err := provider.Builders(ctx, opts)
		require.NoError(t, err)
		require.Same(t, expected, response)
	}
}

func TestBuildersEmptyMockAndError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	backend, err := mock.New(ctx)
	require.NoError(t, err)
	opts := &api.BuildersOpts{State: "head"}
	empty, err := backend.Builders(ctx, opts)
	require.NoError(t, err)
	require.Empty(t, empty.Data)
	expected := &api.Error{StatusCode: 404, Method: "POST", Endpoint: "/eth/v1/beacon/states/head/builders"}
	backend.BuildersFunc = func(context.Context, *api.BuildersOpts) (*api.Response[[]*apiv1.Builder], error) {
		return nil, expected
	}
	svc, err := multi.New(ctx, multi.WithClients([]client.Service{backend}))
	require.NoError(t, err)
	result, err := svc.(client.BuildersProvider).Builders(ctx, opts)
	require.Nil(t, result)
	require.ErrorIs(t, err, expected)
}
