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
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/require"
)

func TestBuildersForwarding(t *testing.T) {
	ctx := context.Background()
	m, err := mock.New(ctx)
	require.NoError(t, err)
	want := &api.Response[[]*apiv1.Builder]{Data: []*apiv1.Builder{{Index: 3}}}
	opts := &api.BuildersOpts{State: "head", IDs: []string{"3"}, Statuses: []string{"active"}}
	m.BuildersFunc = func(_ context.Context, got *api.BuildersOpts) (*api.Response[[]*apiv1.Builder], error) {
		require.Equal(t, opts, got)
		return want, nil
	}
	combined, err := multi.New(ctx, multi.WithLogLevel(zerolog.Disabled), multi.WithClients([]client.Service{m}))
	require.NoError(t, err)
	got, err := combined.(client.BuildersProvider).Builders(ctx, opts)
	require.NoError(t, err)
	require.Equal(t, want, got)
}
