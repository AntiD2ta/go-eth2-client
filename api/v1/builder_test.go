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
	"strings"
	"testing"

	apiv1 "github.com/attestantio/go-eth2-client/api/v1"
	"github.com/stretchr/testify/require"
)

func TestBuilderResponseJSON(t *testing.T) {
	input := fmt.Sprintf(`{"index":"1","status":"active","builder":{"pubkey":"0x%s","version":"1","execution_address":"0x%s","balance":"1","deposit_epoch":"1","withdrawable_epoch":"1"}}`, strings.Repeat("00", 48), strings.Repeat("00", 20))
	var builder apiv1.Builder
	require.NoError(t, json.Unmarshal([]byte(input), &builder))
	require.EqualValues(t, 1, builder.Index)
	require.Equal(t, apiv1.BuilderStatusActive, builder.Status)
	require.EqualValues(t, 1, builder.Builder.Version)
	output, err := json.Marshal(&builder)
	require.NoError(t, err)
	require.JSONEq(t, input, string(output))
}

func TestBuilderResponseRequiresFields(t *testing.T) {
	value := fmt.Sprintf(`{"pubkey":"0x%s","version":"1","execution_address":"0x%s","balance":"1","deposit_epoch":"1","withdrawable_epoch":"1"}`, strings.Repeat("00", 48), strings.Repeat("00", 20))
	for _, input := range []string{
		`{"status":"active","builder":` + value + `}`,
		`{"index":"0","builder":` + value + `}`,
		`{"index":"0","status":"active"}`,
	} {
		var builder apiv1.Builder
		require.Error(t, json.Unmarshal([]byte(input), &builder), input)
	}
}

// TestBuilderResponseBeaconAPIExamples combines the referenced schema examples.
func TestBuilderResponseBeaconAPIExamples(t *testing.T) {
	input := `{"index":"1","status":"active","builder":{"pubkey":"0x93247f2209abcacf57b75a51dafae777f9dd38bc7053d1af526f220a7489a6d3a2753e5f3e8b1cfe39b56f43611df74a","version":"0","execution_address":"0xAbcF8e0d4e9587369b2301D0790347320302cc09","balance":"1","deposit_epoch":"1","withdrawable_epoch":"1"}}`
	var builder apiv1.Builder
	require.NoError(t, json.Unmarshal([]byte(input), &builder))
	require.EqualValues(t, 1, builder.Index)
	require.Equal(t, apiv1.BuilderStatusActive, builder.Status)
	output, err := json.Marshal(&builder)
	require.NoError(t, err)
	var decoded apiv1.Builder
	require.NoError(t, json.Unmarshal(output, &decoded))
	require.Equal(t, builder, decoded)
}
