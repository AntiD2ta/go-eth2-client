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
	"strings"
	"testing"

	"github.com/attestantio/go-eth2-client/spec/gloas"
	"github.com/stretchr/testify/require"
)

func TestBuilderBeaconAPIJSON(t *testing.T) {
	input := fmt.Sprintf(`{"pubkey":"0x%s","version":"7","execution_address":"0x%s","balance":"32","deposit_epoch":"1","withdrawable_epoch":"2"}`, strings.Repeat("11", 48), strings.Repeat("22", 20))
	var builder gloas.Builder
	require.NoError(t, json.Unmarshal([]byte(input), &builder))
	require.Equal(t, uint8(7), builder.Version)
	encoded, err := json.Marshal(&builder)
	require.NoError(t, err)
	require.JSONEq(t, input, string(encoded))
}

func TestBuilderNumericVersionCompatibility(t *testing.T) {
	input := fmt.Sprintf(`{"pubkey":"0x%s","version":7,"execution_address":"0x%s","balance":"32","deposit_epoch":"1","withdrawable_epoch":"2"}`, strings.Repeat("11", 48), strings.Repeat("22", 20))
	var builder gloas.Builder
	require.NoError(t, json.Unmarshal([]byte(input), &builder))
	require.Equal(t, uint8(7), builder.Version)
}
