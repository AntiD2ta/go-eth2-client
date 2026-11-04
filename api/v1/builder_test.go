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

func TestBuilderJSON(t *testing.T) {
	example := fmt.Sprintf(`{"index":"3","status":"active","builder":{"pubkey":"0x%s","version":"0","execution_address":"0x%s","balance":"500","deposit_epoch":"6","withdrawable_epoch":"7"}}`, strings.Repeat("11", 48), strings.Repeat("22", 20))
	var builder apiv1.Builder
	require.NoError(t, json.Unmarshal([]byte(example), &builder))
	require.EqualValues(t, 3, builder.Index)
	require.Equal(t, "active", builder.Status)
	require.EqualValues(t, 500, builder.Builder.Balance)
	encoded, err := json.Marshal(&builder)
	require.NoError(t, err)
	require.JSONEq(t, example, string(encoded))
	for _, field := range []string{"index", "status", "builder"} {
		t.Run(field+"Missing", func(t *testing.T) {
			var fields map[string]json.RawMessage
			require.NoError(t, json.Unmarshal([]byte(example), &fields))
			delete(fields, field)
			invalid, err := json.Marshal(fields)
			require.NoError(t, err)
			require.Error(t, json.Unmarshal(invalid, &builder))
		})
	}
}

func TestBuilderRejectsInvalidIndex(t *testing.T) {
	input := fmt.Sprintf(`{"index":"bad","status":"active","builder":{"pubkey":"0x%s","version":"0","execution_address":"0x%s","balance":"5","deposit_epoch":"6","withdrawable_epoch":"7"}}`, strings.Repeat("11", 48), strings.Repeat("22", 20))
	var builder apiv1.Builder
	require.ErrorContains(t, json.Unmarshal([]byte(input), &builder), "invalid value")
}
