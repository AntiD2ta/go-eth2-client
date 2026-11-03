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

func TestBuilderJSONVersion(t *testing.T) {
	for _, test := range []struct {
		input    string
		expected uint8
		valid    bool
	}{
		{`"9"`, 9, true}, {`"10"`, 10, true}, {`"128"`, 128, true}, {`"255"`, 255, true},
		{`10`, 10, true}, {`"256"`, 0, false}, {`"oops"`, 0, false},
	} {
		t.Run(test.input, func(t *testing.T) {
			input := fmt.Sprintf(`{"pubkey":"0x%s","version":%s,"execution_address":"0x%s","balance":"1","deposit_epoch":"2","withdrawable_epoch":"3"}`, strings.Repeat("11", 48), test.input, strings.Repeat("22", 20))
			var builder gloas.Builder
			err := json.Unmarshal([]byte(input), &builder)
			if !test.valid {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, test.expected, builder.Version)
			output, err := json.Marshal(&builder)
			require.NoError(t, err)
			require.Contains(t, string(output), fmt.Sprintf(`"version":"%d"`, test.expected))
		})
	}
}
