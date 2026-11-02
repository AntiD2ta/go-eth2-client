// Copyright © 2023, 2024 Attestant Limited.
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

// Package v1 contains version 1 API response types.
package v1

import "github.com/attestantio/go-eth2-client/spec/gloas"

// BuilderStatus identifies a builder's status.
type BuilderStatus string

// Builder statuses defined by the beacon API.
//
//nolint:attgo // Builder statuses are wire values and unknown future statuses must round-trip.
const (
	BuilderStatusPending BuilderStatus = "pending"
	BuilderStatusActive  BuilderStatus = "active"
	BuilderStatusExited  BuilderStatus = "exited"
)

// Builder contains a builder and its state registry index and status.
type Builder struct {
	Index   gloas.BuilderIndex `json:"index"`
	Status  BuilderStatus      `json:"status"`
	Builder *gloas.Builder     `json:"builder"`
}
