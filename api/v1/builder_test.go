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
	example := fmt.Sprintf(`{"index":"3","status":"active","builder":{"pubkey":"0x%s","version":"1","execution_address":"0x%s","balance":"12","deposit_epoch":"4","withdrawable_epoch":"5"}}`, strings.Repeat("01", 48), strings.Repeat("02", 20))
	var builder apiv1.Builder
	require.NoError(t, json.Unmarshal([]byte(example), &builder))
	require.EqualValues(t, 3, builder.Index)
	require.Equal(t, apiv1.BuilderStatusActive, builder.Status)
	require.EqualValues(t, 1, builder.Builder.Version)
	require.EqualValues(t, 12, builder.Builder.Balance)
	data, err := json.Marshal(&builder)
	require.NoError(t, err)
	require.JSONEq(t, example, string(data))
}

func TestBuilderResponseRequiresFields(t *testing.T) {
	for _, input := range []string{`{}`, `{"index":"0"}`, `{"index":"0","status":"active"}`} {
		var builder apiv1.Builder
		require.Error(t, json.Unmarshal([]byte(input), &builder))
	}
}

func TestBuilderResponseRejectsEachMissingField(t *testing.T) {
	original := fmt.Sprintf(`{"index":"3","status":"active","builder":{"pubkey":"0x%s","version":"1","execution_address":"0x%s","balance":"12","deposit_epoch":"4","withdrawable_epoch":"5"}}`, strings.Repeat("01", 48), strings.Repeat("02", 20))
	for _, field := range []string{"index", "status", "builder"} {
		t.Run(field, func(t *testing.T) {
			var value map[string]json.RawMessage
			require.NoError(t, json.Unmarshal([]byte(original), &value))
			delete(value, field)
			data, err := json.Marshal(value)
			require.NoError(t, err)
			var decoded apiv1.Builder
			require.Error(t, json.Unmarshal(data, &decoded))
		})
	}
	t.Run("EmptyStatus", func(t *testing.T) {
		var decoded apiv1.Builder
		require.Error(t, json.Unmarshal([]byte(strings.Replace(original, `"status":"active"`, `"status":""`, 1)), &decoded))
	})
	t.Run("InvalidIndex", func(t *testing.T) {
		var decoded apiv1.Builder
		require.Error(t, json.Unmarshal([]byte(strings.Replace(original, `"index":"3"`, `"index":"invalid"`, 1)), &decoded))
	})
}
