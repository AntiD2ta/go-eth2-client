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
	"net/http"
	"testing"

	client "github.com/attestantio/go-eth2-client"
	"github.com/attestantio/go-eth2-client/api"
	"github.com/attestantio/go-eth2-client/spec/gloas"
	"github.com/attestantio/go-eth2-client/spec/phase0"
	"github.com/stretchr/testify/require"
)

func TestGloasBuilders(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	service := testService(ctx, t)
	requireOnGloas(ctx, t, service)
	provider := service.(client.BuildersProvider)
	all, err := provider.Builders(ctx, &api.BuildersOpts{State: "head"})
	if err != nil {
		var apiErr *api.Error
		require.ErrorAs(t, err, &apiErr)
		require.Equal(t, http.StatusNotFound, apiErr.StatusCode)
		t.Log("builders endpoint unsupported or state not found")
		return
	}
	require.NotNil(t, all)
	if len(all.Data) == 0 {
		t.Fatal("no builders available to verify filtering")
	}
	first := all.Data[0]
	for _, opts := range []*api.BuildersOpts{
		{State: "head", Indices: []gloas.BuilderIndex{first.Index}},
		{State: "head", PubKeys: []phase0.BLSPubKey{first.Builder.PublicKey}},
	} {
		selected, err := provider.Builders(ctx, opts)
		require.NoError(t, err)
		require.NotEmpty(t, selected.Data)
		for _, builder := range selected.Data {
			require.Equal(t, first.Index, builder.Index)
			require.Equal(t, first.Builder.PublicKey, builder.Builder.PublicKey)
		}
	}
}
