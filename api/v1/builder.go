// Copyright © 2020 - 2024 Attestant Limited.
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

// Package v1 provides beacon API types.
package v1

import (
	"encoding/json"
	"fmt"

	"github.com/attestantio/go-eth2-client/spec/gloas"
)

// BuilderStatus is a builder's status in the state.
type BuilderStatus uint64

// Builder statuses defined by the beacon API.
const (
	BuilderStatusUnknown BuilderStatus = iota
	BuilderStatusPending
	BuilderStatusActive
	BuilderStatusExited
)

// String returns the beacon API status name.
func (v BuilderStatus) String() string {
	switch v {
	case BuilderStatusPending:
		return "pending"
	case BuilderStatusActive:
		return "active"
	case BuilderStatusExited:
		return "exited"
	default:
		return "unknown"
	}
}

// MarshalJSON implements json.Marshaler.
func (v BuilderStatus) MarshalJSON() ([]byte, error) { return json.Marshal(v.String()) }

// UnmarshalJSON implements json.Unmarshaler.
func (v *BuilderStatus) UnmarshalJSON(input []byte) error {
	var status string
	if err := json.Unmarshal(input, &status); err != nil {
		return err
	}
	switch status {
	case "pending":
		*v = BuilderStatusPending
	case "active":
		*v = BuilderStatusActive
	case "exited":
		*v = BuilderStatusExited
	default:
		return fmt.Errorf("unrecognised builder status %q", status)
	}

	return nil
}

// Builder contains a builder and its index and status.
type Builder struct {
	Index   gloas.BuilderIndex `json:"index"`
	Status  BuilderStatus      `json:"status"`
	Builder *gloas.Builder     `json:"builder"`
}
