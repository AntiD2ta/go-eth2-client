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
	input := fmt.Sprintf(`{"index":"1","status":"active","builder":{"pubkey":"0x%s","version":"0","execution_address":"0x%s","balance":"1","deposit_epoch":"1","withdrawable_epoch":"1"}}`, strings.Repeat("11", 48), strings.Repeat("22", 20))
	var builder apiv1.Builder
	require.NoError(t, json.Unmarshal([]byte(input), &builder))
	require.EqualValues(t, 1, builder.Index)
	require.Equal(t, "active", builder.Status)
	require.NotNil(t, builder.Builder)
	require.EqualValues(t, 1, builder.Builder.Balance)
	data, err := json.Marshal(&builder)
	require.NoError(t, err)
	require.JSONEq(t, input, string(data))
}
