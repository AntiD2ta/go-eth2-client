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

// Package v1 contains types returned by the beacon API.
package v1

import (
	"encoding/json"
	"errors"

	"github.com/attestantio/go-eth2-client/spec/gloas"
)

// BuilderStatus is a builder's status in a beacon state.
type BuilderStatus string

// Builder statuses defined by the beacon API. String values also allow future statuses.
const (
	BuilderStatusPending BuilderStatus = "pending" //nolint:attgo // Preserve unknown wire values.
	BuilderStatusActive  BuilderStatus = "active"  //nolint:attgo // Preserve unknown wire values.
	BuilderStatusExited  BuilderStatus = "exited"  //nolint:attgo // Preserve unknown wire values.
)

// Builder contains a builder's index, status and consensus record.
type Builder struct {
	Index   gloas.BuilderIndex `json:"index"`
	Status  BuilderStatus      `json:"status"`
	Builder *gloas.Builder     `json:"builder"`
}

// UnmarshalJSON implements json.Unmarshaler.
func (b *Builder) UnmarshalJSON(input []byte) error {
	var data struct {
		Index   *gloas.BuilderIndex `json:"index"`
		Status  *BuilderStatus      `json:"status"`
		Builder *gloas.Builder      `json:"builder"`
	}
	if err := json.Unmarshal(input, &data); err != nil {
		return err
	}
	if data.Index == nil {
		return errors.New("index missing")
	}
	if data.Status == nil || *data.Status == "" {
		return errors.New("status missing")
	}
	if data.Builder == nil {
		return errors.New("builder missing")
	}
	b.Index = *data.Index
	b.Status = *data.Status
	b.Builder = data.Builder

	return nil
}
