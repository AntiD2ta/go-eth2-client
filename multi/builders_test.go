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

func TestBuildersProvider(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m, err := mock.New(ctx)
	require.NoError(t, err)
	provider, ok := any(m).(client.BuildersProvider)
	require.True(t, ok)
	empty, err := provider.Builders(ctx, &api.BuildersOpts{State: "head"})
	require.NoError(t, err)
	require.Empty(t, empty.Data)
	want := &api.Response[[]*apiv1.Builder]{Data: []*apiv1.Builder{{Index: 3}}}
	m.BuildersFunc = func(_ context.Context, opts *api.BuildersOpts) (*api.Response[[]*apiv1.Builder], error) {
		require.Equal(t, "head", opts.State)
		return want, nil
	}
	service, err := multi.New(ctx, multi.WithClients([]client.Service{m}))
	require.NoError(t, err)
	multiProvider, ok := service.(client.BuildersProvider)
	require.True(t, ok)
	got, err := multiProvider.Builders(ctx, &api.BuildersOpts{State: "head"})
	require.NoError(t, err)
	require.Equal(t, want, got)
}

func TestBuildersTestClients(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m, err := mock.New(ctx)
	require.NoError(t, err)
	expected := &api.Response[[]*apiv1.Builder]{Data: []*apiv1.Builder{{Index: 3}}}
	m.BuildersFunc = func(_ context.Context, _ *api.BuildersOpts) (*api.Response[[]*apiv1.Builder], error) {
		return expected, nil
	}
	erroring, err := testclients.NewErroring(ctx, 0, m)
	require.NoError(t, err)
	sleepy, err := testclients.NewSleepy(ctx, 0, time.Millisecond, m)
	require.NoError(t, err)
	for _, service := range []client.Service{erroring, sleepy} {
		provider, ok := service.(client.BuildersProvider)
		require.True(t, ok)
		got, err := provider.Builders(ctx, &api.BuildersOpts{State: "head"})
		require.NoError(t, err)
		require.Equal(t, expected, got)
	}
}
