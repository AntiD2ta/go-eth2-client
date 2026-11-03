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

// Package api contains options and response types for the client.
package api

// BuildersOpts are the options for obtaining builders from a state.
type BuildersOpts struct {
	Common CommonOpts
	// State is a slot, state root, or a state identifier such as head.
	State string
	// IDs are builder indices or 0x-prefixed public keys. Empty means all builders.
	IDs []string
	// Statuses filters by builder status. Empty means all statuses.
	Statuses []string
}
