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

package api

import (
	apiv1 "github.com/attestantio/go-eth2-client/api/v1"
	"github.com/attestantio/go-eth2-client/spec/gloas"
	"github.com/attestantio/go-eth2-client/spec/phase0"
)

// BuildersOpts are the options for obtaining builders.
type BuildersOpts struct {
	Common CommonOpts
	// State identifies the beacon state.
	State string
	// Indices and PubKeys select builders; empty lists select all builders.
	Indices []gloas.BuilderIndex
	PubKeys []phase0.BLSPubKey
	// Statuses selects builder statuses; an empty list selects all statuses.
	Statuses []apiv1.BuilderStatus
}
