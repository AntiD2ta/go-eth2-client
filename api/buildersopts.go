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

// Package api contains options and responses for the client API.
package api

import apiv1 "github.com/attestantio/go-eth2-client/api/v1"

// BuildersOpts are the options for obtaining builders.
type BuildersOpts struct {
	Common CommonOpts
	// State identifies the state to query.
	State string
	// IDs contains decimal builder indices or hex-encoded public keys.
	IDs []string
	// Statuses filters builder statuses.
	Statuses []apiv1.BuilderStatus
}
