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
	"testing"

	apiv1 "github.com/attestantio/go-eth2-client/api/v1"
	"github.com/stretchr/testify/require"
)

func TestBuilderResponseJSON(t *testing.T) {
	data, err := os.ReadFile("testdata/builder-response.json")
	require.NoError(t, err)
	var builder apiv1.Builder
	require.NoError(t, json.Unmarshal(data, &builder))
	require.EqualValues(t, 1, builder.Index)
	require.EqualValues(t, "active", builder.Status)
	require.NotNil(t, builder.Builder)
	require.EqualValues(t, 1, builder.Builder.Balance)
	encoded, err := json.Marshal(&builder)
	require.NoError(t, err)
	var roundTrip apiv1.Builder
	require.NoError(t, json.Unmarshal(encoded, &roundTrip))
	require.Equal(t, builder, roundTrip)
	var jsonFields map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(encoded, &jsonFields))
	require.JSONEq(t, `"1"`, string(jsonFields["index"]))
}
