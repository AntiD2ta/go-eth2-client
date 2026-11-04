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

import "github.com/attestantio/go-eth2-client/spec/gloas"

// Builder contains a builder's index, status and consensus data.
type Builder struct {
	Index   gloas.BuilderIndex `json:"index"`
	Status  BuilderStatus      `json:"status"`
	Builder *gloas.Builder     `json:"builder"`
}

// BuilderStatus is a builder's status in the state.
type BuilderStatus string

// Builder statuses defined by the beacon API.
const (
	BuilderStatusPending BuilderStatus = "pending" //nolint:attgo // Beacon API statuses are strings.
	BuilderStatusActive  BuilderStatus = "active"  //nolint:attgo // Beacon API statuses are strings.
	BuilderStatusExited  BuilderStatus = "exited"  //nolint:attgo // Beacon API statuses are strings.
)
