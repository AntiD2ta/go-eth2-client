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
	input := fmt.Sprintf(`{"index":"3","status":"active","builder":{"pubkey":"0x%s","version":"1","execution_address":"0x%s","balance":"32000000000","deposit_epoch":"4","withdrawable_epoch":"5"}}`, strings.Repeat("01", 48), strings.Repeat("02", 20))
	var got apiv1.Builder
	require.NoError(t, json.Unmarshal([]byte(input), &got))
	require.EqualValues(t, 3, got.Index)
	require.Equal(t, "active", got.Status)
	require.EqualValues(t, 1, got.Builder.Version)
	data, err := json.Marshal(&got)
	require.NoError(t, err)
	require.JSONEq(t, input, string(data))
}

func TestBuilderResponseMissingFields(t *testing.T) {
	for _, input := range []string{`{}`, `{"index":"1"}`, `{"index":"1","status":"active"}`} {
		var got apiv1.Builder
		require.Error(t, json.Unmarshal([]byte(input), &got))
	}
}
