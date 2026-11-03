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
	apiv1 "github.com/attestantio/go-eth2-client/api/v1"
	"github.com/attestantio/go-eth2-client/spec/gloas"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestBuilderResponseJSON(t *testing.T) {
	input := fmt.Sprintf(`{"index":"3","status":"active","builder":{"pubkey":"0x%096x","version":"7","execution_address":"0x%040x","balance":"3","deposit_epoch":"4","withdrawable_epoch":"5"}}`, 1, 2)
	var response apiv1.Builder
	require.NoError(t, json.Unmarshal([]byte(input), &response))
	require.Equal(t, gloas.BuilderIndex(3), response.Index)
	require.Equal(t, apiv1.BuilderStatusActive, response.Status)
	require.Equal(t, uint8(7), response.Builder.Version)
	encoded, err := json.Marshal(&response)
	require.NoError(t, err)
	require.JSONEq(t, input, string(encoded))
}
