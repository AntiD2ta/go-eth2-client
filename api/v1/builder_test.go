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

func TestBuilderJSON(t *testing.T) {
	// BuilderResponse has no object example; these values use its component examples.
	input, err := os.ReadFile("testdata/builder_response.json")
	require.NoError(t, err)
	var builder apiv1.Builder
	require.NoError(t, json.Unmarshal(input, &builder))
	require.EqualValues(t, 3, builder.Index)
	require.Equal(t, "active", builder.Status)
	require.EqualValues(t, 0, builder.Builder.Version)
	output, err := json.Marshal(&builder)
	require.NoError(t, err)
	require.JSONEq(t, string(input), string(output))
}
