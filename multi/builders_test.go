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
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/require"
)

func TestBuildersProvider(t *testing.T) {
	ctx := context.Background()
	mockService, err := mock.New(ctx)
	require.NoError(t, err)
	provider, ok := any(mockService).(client.BuildersProvider)
	require.True(t, ok, "mock must implement BuildersProvider")
	result, err := provider.Builders(ctx, &api.BuildersOpts{State: "head"})
	require.NoError(t, err)
	require.Empty(t, result.Data)
	expected := &api.Response[[]*apiv1.Builder]{Data: []*apiv1.Builder{{Index: gloas.BuilderIndex(3), Status: apiv1.BuilderStatusActive}}}
	opts := &api.BuildersOpts{State: "head", IDs: []string{"3"}}
	mockService.BuildersFunc = func(_ context.Context, got *api.BuildersOpts) (*api.Response[[]*apiv1.Builder], error) {
		require.Equal(t, opts, got)
		return expected, nil
	}
	result, err = provider.Builders(ctx, opts)
	require.NoError(t, err)
	require.Same(t, expected, result)
	service, err := multi.New(ctx, multi.WithLogLevel(zerolog.Disabled), multi.WithClients([]client.Service{mockService}))
	require.NoError(t, err)
	provider, ok = service.(client.BuildersProvider)
	require.True(t, ok, "multi must implement BuildersProvider")
	result, err = provider.Builders(ctx, opts)
	require.NoError(t, err)
	require.Same(t, expected, result)
}
