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

// Builder contains a builder with its index and status.
type Builder struct {
	Index   gloas.BuilderIndex `json:"index"`
	Status  string             `json:"status"`
	Builder *gloas.Builder     `json:"builder"`
}

// UnmarshalJSON implements json.Unmarshaler.
func (v *Builder) UnmarshalJSON(input []byte) error {
	var data struct {
		Index   *gloas.BuilderIndex `json:"index"`
		Status  string              `json:"status"`
		Builder *gloas.Builder      `json:"builder"`
	}
	if err := json.Unmarshal(input, &data); err != nil {
		return err
	}
	if data.Index == nil {
		return errors.New("index missing")
	}
	if data.Status == "" {
		return errors.New("status missing")
	}
	if data.Builder == nil {
		return errors.New("builder missing")
	}
	v.Index = *data.Index
	v.Status = data.Status
	v.Builder = data.Builder

	return nil
}
