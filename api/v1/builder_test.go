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
	"os"
	"strings"
	"testing"

	apiv1 "github.com/attestantio/go-eth2-client/api/v1"
	"github.com/stretchr/testify/require"
)

func TestBuilderJSON(t *testing.T) {
	input, err := os.ReadFile("testdata/builder_response.json")
	require.NoError(t, err)
	var builder apiv1.Builder
	require.NoError(t, json.Unmarshal(input, &builder))
	require.EqualValues(t, 1, builder.Index)
	require.Equal(t, "active", builder.Status)
	require.EqualValues(t, 1, builder.Builder.Balance)
	output, err := json.Marshal(&builder)
	require.NoError(t, err)
	// Addresses are normalized to lowercase by the existing builder codec.
	require.JSONEq(t, strings.ToLower(string(input)), strings.ToLower(string(output)))
	legacy := strings.Replace(string(input), `"version": "0"`, `"version": 0`, 1)
	require.NotEqual(t, string(input), legacy)
	require.NoError(t, json.Unmarshal([]byte(legacy), &builder))
	for _, invalid := range []string{`{}`, `{"index":"1","status":"active"}`, strings.Replace(string(input), `"version": "0"`, `"version": "256"`, 1)} {
		require.Error(t, json.Unmarshal([]byte(invalid), &builder))
	}
}
