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

package client_test

import (
	"context"
	"testing"

	client "github.com/attestantio/go-eth2-client"
	"github.com/attestantio/go-eth2-client/api"
	apiv1 "github.com/attestantio/go-eth2-client/api/v1"
	"github.com/attestantio/go-eth2-client/mock"
	"github.com/attestantio/go-eth2-client/multi"
	"github.com/stretchr/testify/require"
)

func TestBuildersMockAndMulti(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m, err := mock.New(ctx)
	require.NoError(t, err)
	expected := &api.Response[[]*apiv1.Builder]{Data: []*apiv1.Builder{{Index: 3}}}
	opts := &api.BuildersOpts{State: "head"}
	m.BuildersFunc = func(_ context.Context, got *api.BuildersOpts) (*api.Response[[]*apiv1.Builder], error) {
		require.Equal(t, opts, got)
		return expected, nil
	}
	for _, name := range []string{"mock", "multi"} {
		t.Run(name, func(t *testing.T) {
			var provider client.BuildersProvider = m
			if name == "multi" {
				s, e := multi.New(ctx, multi.WithClients([]client.Service{m}))
				require.NoError(t, e)
				provider = s.(client.BuildersProvider)
			}
			got, e := provider.Builders(ctx, opts)
			require.NoError(t, e)
			require.Equal(t, expected, got)
		})
	}
}
