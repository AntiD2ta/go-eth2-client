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

package v1

import (
	"encoding/json"
	"errors"

	"github.com/attestantio/go-eth2-client/spec/gloas"
)

// Builder contains a builder and its index and status in the state.
type Builder struct {
	Index   gloas.BuilderIndex `json:"index"`
	Status  string             `json:"status"`
	Builder *gloas.Builder     `json:"builder"`
}

// UnmarshalJSON implements json.Unmarshaler.
func (v *Builder) UnmarshalJSON(input []byte) error {
	var decoded struct {
		Index   *gloas.BuilderIndex `json:"index"`
		Status  string              `json:"status"`
		Builder *gloas.Builder      `json:"builder"`
	}
	if err := json.Unmarshal(input, &decoded); err != nil {
		return err
	}
	if decoded.Index == nil {
		return errors.New("index missing")
	}
	if decoded.Status == "" {
		return errors.New("status missing")
	}
	if decoded.Builder == nil {
		return errors.New("builder missing")
	}
	v.Index, v.Status, v.Builder = *decoded.Index, decoded.Status, decoded.Builder

	return nil
}
