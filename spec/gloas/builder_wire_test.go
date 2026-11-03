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

package gloas_test

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/attestantio/go-eth2-client/spec/gloas"
	"github.com/stretchr/testify/require"
)

func TestBuilderSpecVersionJSON(t *testing.T) {
	input := fmt.Sprintf(`{"pubkey":"0x%096x","version":"7","execution_address":"0x%040x","balance":"3","deposit_epoch":"4","withdrawable_epoch":"5"}`, 1, 2)
	var builder gloas.Builder
	require.NoError(t, json.Unmarshal([]byte(input), &builder))
	require.Equal(t, uint8(7), builder.Version)
	encoded, err := json.Marshal(&builder)
	require.NoError(t, err)
	require.JSONEq(t, input, string(encoded))
}

func TestBuilderLegacyNumericVersionJSON(t *testing.T) {
	input := fmt.Sprintf(`{"pubkey":"0x%096x","version":7,"execution_address":"0x%040x","balance":"3","deposit_epoch":"4","withdrawable_epoch":"5"}`, 1, 2)
	var builder gloas.Builder
	require.NoError(t, json.Unmarshal([]byte(input), &builder))
	require.Equal(t, uint8(7), builder.Version)
}

func TestBuilderVersionBounds(t *testing.T) {
	for _, version := range []string{`"128"`, `"255"`, `255`} {
		t.Run(version, func(t *testing.T) {
			input := fmt.Sprintf(`{"pubkey":"0x%096x","version":%s,"execution_address":"0x%040x","balance":"3","deposit_epoch":"4","withdrawable_epoch":"5"}`, 1, version, 2)
			var builder gloas.Builder
			require.NoError(t, json.Unmarshal([]byte(input), &builder))
			data, err := json.Marshal(&builder)
			require.NoError(t, err)
			var roundTrip gloas.Builder
			require.NoError(t, json.Unmarshal(data, &roundTrip))
			require.Equal(t, builder, roundTrip)
		})
	}
	for _, version := range []string{`"256"`, `256`, `"-1"`, `true`, `"abc"`} {
		t.Run("Invalid"+version, func(t *testing.T) {
			input := fmt.Sprintf(`{"pubkey":"0x%096x","version":%s,"execution_address":"0x%040x","balance":"3","deposit_epoch":"4","withdrawable_epoch":"5"}`, 1, version, 2)
			var builder gloas.Builder
			require.Error(t, json.Unmarshal([]byte(input), &builder))
		})
	}
}
