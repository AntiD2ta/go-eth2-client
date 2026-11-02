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
	"github.com/attestantio/go-eth2-client/spec/gloas"
	"github.com/attestantio/go-eth2-client/testclients"
	"github.com/stretchr/testify/require"
)

func TestBuilders(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	backend, err := mock.New(ctx)
	require.NoError(t, err)
	expected := &api.Response[map[gloas.BuilderIndex]*apiv1.Builder]{Data: map[gloas.BuilderIndex]*apiv1.Builder{3: {Index: 3, Status: "active", Builder: &gloas.Builder{}}}}
	opts := &api.BuildersOpts{State: "head", Indices: []gloas.BuilderIndex{3}}
	backend.BuildersFunc = func(_ context.Context, got *api.BuildersOpts) (*api.Response[map[gloas.BuilderIndex]*apiv1.Builder], error) {
		require.Equal(t, opts, got)
		return expected, nil
	}
	direct, err := backend.Builders(ctx, opts)
	require.NoError(t, err)
	require.Equal(t, expected, direct)
	service, err := multi.New(ctx, multi.WithClients([]client.Service{backend}))
	require.NoError(t, err)
	response, err := service.(client.BuildersProvider).Builders(ctx, opts)
	require.NoError(t, err)
	require.Equal(t, expected, response)
}

func TestBuildersWithTestClients(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	backend, err := mock.New(ctx)
	require.NoError(t, err)
	failing, err := testclients.NewErroring(ctx, 1, backend)
	require.NoError(t, err)
	slow, err := testclients.NewSleepy(ctx, 0, time.Millisecond, backend)
	require.NoError(t, err)
	service, err := multi.New(ctx, multi.WithClients([]client.Service{failing, slow}))
	require.NoError(t, err)
	result, err := service.(client.BuildersProvider).Builders(ctx, &api.BuildersOpts{State: "head"})
	require.NoError(t, err)
	require.Empty(t, result.Data)
}
