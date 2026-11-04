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

func TestBuildersForwarding(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	mockClient, err := mock.New(ctx)
	require.NoError(t, err)
	expected := &api.Response[map[gloas.BuilderIndex]*apiv1.Builder]{Data: map[gloas.BuilderIndex]*apiv1.Builder{3: {Index: 3, Status: apiv1.BuilderStatusActive}}}
	opts := &api.BuildersOpts{State: "head", Indices: []gloas.BuilderIndex{3}}
	mockClient.BuildersFunc = func(_ context.Context, received *api.BuildersOpts) (*api.Response[map[gloas.BuilderIndex]*apiv1.Builder], error) {
		require.Equal(t, opts, received)
		return expected, nil
	}
	multiClient, err := multi.New(ctx, multi.WithClients([]client.Service{mockClient}))
	require.NoError(t, err)
	result, err := multiClient.(client.BuildersProvider).Builders(ctx, opts)
	require.NoError(t, err)
	require.Equal(t, expected, result)
}

func TestBuildersTestClientForwarding(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	base, err := mock.New(ctx)
	require.NoError(t, err)
	erroring, err := testclients.NewErroring(ctx, 0, base)
	require.NoError(t, err)
	sleepy, err := testclients.NewSleepy(ctx, 0, time.Millisecond, base)
	require.NoError(t, err)
	for _, wrapped := range []client.Service{erroring, sleepy} {
		provider, ok := wrapped.(client.BuildersProvider)
		require.True(t, ok)
		response, err := provider.Builders(ctx, &api.BuildersOpts{State: "head"})
		require.NoError(t, err)
		require.Empty(t, response.Data)
	}
}
