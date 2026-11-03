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

// Package http implements a beacon API client.
package http

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"

	client "github.com/attestantio/go-eth2-client"
	"github.com/attestantio/go-eth2-client/api"
	apiv1 "github.com/attestantio/go-eth2-client/api/v1"
)

// Builders obtains builders from the given state.
func (s *Service) Builders(ctx context.Context, opts *api.BuildersOpts) (*api.Response[[]*apiv1.Builder], error) {
	if err := s.assertIsActive(ctx); err != nil {
		return nil, err
	}
	if opts == nil {
		return nil, client.ErrNoOptions
	}
	if opts.State == "" {
		return nil, errors.Join(errors.New("no state specified"), client.ErrInvalidOptions)
	}
	body, err := json.Marshal(struct {
		IDs      []string `json:"ids,omitempty"`
		Statuses []string `json:"statuses,omitempty"`
	}{IDs: opts.IDs, Statuses: opts.Statuses})
	if err != nil {
		return nil, errors.Join(errors.New("failed to marshal request data"), err)
	}
	endpoint := fmt.Sprintf("/eth/v1/beacon/states/%s/builders", opts.State)
	response, err := s.post(ctx, endpoint, "", &opts.Common, bytes.NewReader(body), ContentTypeJSON, map[string]string{})
	if err != nil {
		return nil, err
	}
	data, metadata, err := decodeJSONResponse(bytes.NewReader(response.body), []*apiv1.Builder{})
	if err != nil {
		return nil, err
	}

	return &api.Response[[]*apiv1.Builder]{Data: data, Metadata: metadata}, nil
}
