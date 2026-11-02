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

package http_test

import (
	"context"
	client "github.com/attestantio/go-eth2-client"
	"github.com/attestantio/go-eth2-client/api"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestGloasBuilders(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	service := testService(ctx, t).(client.Service)
	requireOnGloas(ctx, t, service)
	provider := service.(client.BuildersProvider)
	response, err := provider.Builders(ctx, &api.BuildersOpts{State: "head", IDs: []string{"3"}})
	if err != nil {
		var apiErr *api.Error
		require.ErrorAs(t, err, &apiErr)
		require.Equal(t, 404, apiErr.StatusCode)
		require.Nil(t, response)
		return
	}
	require.NotNil(t, response)
	for _, builder := range response.Data {
		require.NotNil(t, builder.Builder)
		require.EqualValues(t, 3, builder.Index)
		filtered, err := provider.Builders(ctx, &api.BuildersOpts{State: "head", IDs: []string{builder.Builder.PublicKey.String()}, Statuses: []string{builder.Status}})
		require.NoError(t, err)
		require.Len(t, filtered.Data, 1)
		require.Equal(t, builder.Index, filtered.Data[0].Index)
	}
}
