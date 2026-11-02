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

// Package api provides options and responses for beacon node APIs.
package api

import apiv1 "github.com/attestantio/go-eth2-client/api/v1"

// BuildersOpts are the options for obtaining builders from a state.
type BuildersOpts struct {
	Common CommonOpts
	// State is a slot, state root, or one of genesis, head, justified, or finalized.
	State string
	// IDs contains decimal indices or 0x-prefixed public keys. Empty means all builders.
	IDs []string
	// Statuses restricts the results. Empty means all statuses.
	Statuses []apiv1.BuilderStatus
}
