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

package v1_test

import (
	"encoding/json"
	"fmt"

	"testing"

	apiv1 "github.com/attestantio/go-eth2-client/api/v1"
	"github.com/attestantio/go-eth2-client/spec/gloas"
	"github.com/stretchr/testify/require"
)

func TestBuilderResponseJSON(t *testing.T) {
	// The example values come from BuilderResponse and its referenced primitive schemas.
	input := fmt.Sprintf(`{"index":"1","status":"active","builder":{"pubkey":%q,"version":"0","execution_address":%q,"balance":"1","deposit_epoch":"1","withdrawable_epoch":"1"}}`, "0x93247f2209abcacf57b75a51dafae777f9dd38bc7053d1af526f220a7489a6d3a2753e5f3e8b1cfe39b56f43611df74a", "0xAbcF8e0d4e9587369b2301D0790347320302cc09")
	var builder apiv1.Builder
	require.NoError(t, json.Unmarshal([]byte(input), &builder))
	require.Equal(t, gloas.BuilderIndex(1), builder.Index)
	require.Equal(t, "active", builder.Status)
	require.EqualValues(t, 1, builder.Builder.Balance)
	output, err := json.Marshal(&builder)
	require.NoError(t, err)
	var roundTrip apiv1.Builder
	require.NoError(t, json.Unmarshal(output, &roundTrip))
	require.Equal(t, builder, roundTrip)
	require.Contains(t, string(output), `"version":"0"`)
}
