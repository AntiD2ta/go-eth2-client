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

// Builder contains a builder's index, status, and consensus record.
type Builder struct {
	Index   gloas.BuilderIndex `json:"index"`
	Status  string             `json:"status"`
	Builder *gloas.Builder     `json:"builder"`
}

// UnmarshalJSON implements json.Unmarshaler.
func (b *Builder) UnmarshalJSON(input []byte) error {
	var wire struct {
		Index   *gloas.BuilderIndex `json:"index"`
		Status  string              `json:"status"`
		Builder *gloas.Builder      `json:"builder"`
	}
	if err := json.Unmarshal(input, &wire); err != nil {
		return err
	}
	if wire.Index == nil {
		return errors.New("index missing")
	}
	if wire.Status == "" {
		return errors.New("status missing")
	}
	if wire.Builder == nil {
		return errors.New("builder missing")
	}
	b.Index, b.Status, b.Builder = *wire.Index, wire.Status, wire.Builder

	return nil
}
