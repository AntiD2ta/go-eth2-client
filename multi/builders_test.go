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

func TestBuildersProviders(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	mockClient, err := mock.New(ctx, mock.WithName("builder-mock"))
	require.NoError(t, err)
	opts := &api.BuildersOpts{State: "head", Statuses: []string{"active"}}
	want := &api.Response[[]*apiv1.Builder]{Data: []*apiv1.Builder{{Index: 3, Status: "active"}}}
	mockClient.BuildersFunc = func(_ context.Context, got *api.BuildersOpts) (*api.Response[[]*apiv1.Builder], error) {
		require.Equal(t, opts, got)
		return want, nil
	}
	errClient, err := testclients.NewErroring(ctx, 0, mockClient)
	require.NoError(t, err)
	sleepyClient, err := testclients.NewSleepy(ctx, 0, time.Millisecond, mockClient)
	require.NoError(t, err)
	multiClient, err := multi.New(ctx, multi.WithClients([]client.Service{mockClient}))
	require.NoError(t, err)
	for _, service := range []client.Service{mockClient, errClient, sleepyClient, multiClient} {
		provider, ok := service.(client.BuildersProvider)
		require.True(t, ok, "%T must provide builders", service)
		result, callErr := provider.Builders(ctx, opts)
		require.NoError(t, callErr)
		require.Equal(t, want, result)
	}
}
