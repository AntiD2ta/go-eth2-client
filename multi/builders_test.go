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
	defaultResponse, err := client.BuildersProvider(m).Builders(ctx, &api.BuildersOpts{})
	require.NoError(t, err)
	require.Empty(t, defaultResponse.Data)
	opts := &api.BuildersOpts{State: "head", IDs: []string{"3"}}
	m.BuildersFunc = func(_ context.Context, got *api.BuildersOpts) (*api.Response[[]*apiv1.Builder], error) {
		require.Equal(t, opts, got)
		return &api.Response[[]*apiv1.Builder]{Data: []*apiv1.Builder{{Index: 3, Status: "active"}}}, nil
	}
	wrapped, err := testclients.NewSleepy(ctx, 0, time.Millisecond, m)
	require.NoError(t, err)
	wrapped, err = testclients.NewErroring(ctx, 0, wrapped)
	require.NoError(t, err)
	s, err := multi.New(ctx, multi.WithClients([]client.Service{wrapped}))
	require.NoError(t, err)
	res, err := s.(client.BuildersProvider).Builders(ctx, opts)
	require.NoError(t, err)
	require.EqualValues(t, 3, res.Data[0].Index)
}
