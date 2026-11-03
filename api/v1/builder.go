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

// BuilderStatus is the status of a builder in the state.
type BuilderStatus string

const (
	// BuilderStatusPending identifies a pending builder.
	BuilderStatusPending BuilderStatus = "pending" //nolint:attgo // The API uses string statuses.
	// BuilderStatusActive identifies an active builder.
	BuilderStatusActive BuilderStatus = "active" //nolint:attgo // The API uses string statuses.
	// BuilderStatusExited identifies an exited builder.
	BuilderStatusExited BuilderStatus = "exited" //nolint:attgo // The API uses string statuses.
)

// Builder contains a builder with its index and status.
type Builder struct {
	Index   gloas.BuilderIndex `json:"index"`
	Status  BuilderStatus      `json:"status"`
	Builder *gloas.Builder     `json:"builder"`
}
