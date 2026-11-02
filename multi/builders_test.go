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

func TestBuilders(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m, err := mock.New(ctx)
	require.NoError(t, err)
	var _ client.BuildersProvider = m
	opts := &api.BuildersOpts{State: "head"}
	expected := &api.Response[[]*apiv1.Builder]{Data: []*apiv1.Builder{{Index: 3}}}
	m.BuildersFunc = func(_ context.Context, got *api.BuildersOpts) (*api.Response[[]*apiv1.Builder], error) {
		require.Equal(t, opts, got)
		return expected, nil
	}
	service, err := multi.New(ctx, multi.WithClients([]client.Service{m}))
	require.NoError(t, err)
	response, err := service.(client.BuildersProvider).Builders(ctx, opts)
	require.NoError(t, err)
	require.Equal(t, expected, response)
}

type serviceWithoutBuilders struct{ client.Service }

func TestBuildersUnsupportedClient(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m, err := mock.New(ctx)
	require.NoError(t, err)
	service, err := multi.New(ctx, multi.WithClients([]client.Service{serviceWithoutBuilders{m}}))
	require.NoError(t, err)
	_, err = service.(client.BuildersProvider).Builders(ctx, &api.BuildersOpts{State: "head"})
	require.Error(t, err)
}

func TestBuildersTestClients(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m, err := mock.New(ctx)
	require.NoError(t, err)
	expected := &api.Response[[]*apiv1.Builder]{Data: []*apiv1.Builder{{Index: 3}}}
	m.BuildersFunc = func(context.Context, *api.BuildersOpts) (*api.Response[[]*apiv1.Builder], error) {
		return expected, nil
	}
	erroring, err := testclients.NewErroring(ctx, 0, m)
	require.NoError(t, err)
	sleepy, err := testclients.NewSleepy(ctx, 0, time.Millisecond, m)
	require.NoError(t, err)
	for _, c := range []client.Service{erroring, sleepy} {
		p, ok := c.(client.BuildersProvider)
		require.True(t, ok)
		result, err := p.Builders(ctx, &api.BuildersOpts{State: "head"})
		require.NoError(t, err)
		require.Equal(t, expected, result)
	}
}
