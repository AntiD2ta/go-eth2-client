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
	client "github.com/attestantio/go-eth2-client"
	"github.com/attestantio/go-eth2-client/api"
	apiv1 "github.com/attestantio/go-eth2-client/api/v1"
	"github.com/attestantio/go-eth2-client/mock"
	"github.com/attestantio/go-eth2-client/multi"
	"github.com/attestantio/go-eth2-client/spec/gloas"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestBuildersForwarded(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	mockClient, err := mock.New(ctx)
	require.NoError(t, err)
	opts := &api.BuildersOpts{State: "head", Indices: []gloas.BuilderIndex{3}}
	expected := &api.Response[[]*apiv1.Builder]{Data: []*apiv1.Builder{{Index: 3, Status: "active"}}}
	mockClient.BuildersFunc = func(_ context.Context, got *api.BuildersOpts) (*api.Response[[]*apiv1.Builder], error) {
		require.Equal(t, opts, got)
		return expected, nil
	}
	service, err := multi.New(ctx, multi.WithClients([]client.Service{mockClient}))
	require.NoError(t, err)
	result, err := service.(client.BuildersProvider).Builders(ctx, opts)
	require.NoError(t, err)
	require.Equal(t, expected, result)
}

func TestBuildersMockDefault(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	service, err := mock.New(ctx)
	require.NoError(t, err)
	result, err := service.Builders(ctx, &api.BuildersOpts{State: "head"})
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Empty(t, result.Data)
}
