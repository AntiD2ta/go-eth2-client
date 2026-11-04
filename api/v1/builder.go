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

// BuilderStatus is a builder's status in the registry.
type BuilderStatus string

// Builder status values defined by the beacon API.
const (
	BuilderStatusPending BuilderStatus = "pending" //nolint:attgo // Beacon API status values are strings.
	BuilderStatusActive  BuilderStatus = "active"  //nolint:attgo // Beacon API status values are strings.
	BuilderStatusExited  BuilderStatus = "exited"  //nolint:attgo // Beacon API status values are strings.
)

// Builder contains the registry builder and its index and status.
type Builder struct {
	Index   gloas.BuilderIndex `json:"index"`
	Status  BuilderStatus      `json:"status"`
	Builder *gloas.Builder     `json:"builder"`
}

// UnmarshalJSON implements json.Unmarshaler.
func (v *Builder) UnmarshalJSON(input []byte) error {
	var decoded struct {
		Index   *gloas.BuilderIndex `json:"index"`
		Status  *BuilderStatus      `json:"status"`
		Builder *gloas.Builder      `json:"builder"`
	}
	if err := json.Unmarshal(input, &decoded); err != nil {
		return err
	}
	if decoded.Index == nil {
		return errors.New("index missing")
	}
	if decoded.Status == nil || *decoded.Status == "" {
		return errors.New("status missing")
	}
	if decoded.Builder == nil {
		return errors.New("builder missing")
	}
	v.Index = *decoded.Index
	v.Status = *decoded.Status
	v.Builder = decoded.Builder

	return nil
}
