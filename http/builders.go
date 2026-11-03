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
	"go.opentelemetry.io/otel"
)

// Builders provides builders from a state.
func (s *Service) Builders(ctx context.Context, opts *api.BuildersOpts) (*api.Response[[]*apiv1.Builder], error) {
	ctx, span := otel.Tracer("attestantio.go-eth2-client.http").Start(ctx, "Builders")
	defer span.End()
	if err := s.assertIsActive(ctx); err != nil {
		return nil, err
	}
	if opts == nil {
		return nil, client.ErrNoOptions
	}
	if opts.State == "" {
		return nil, errors.Join(errors.New("no state specified"), client.ErrInvalidOptions)
	}
	body := struct {
		IDs      []string `json:"ids,omitempty"`
		Statuses []string `json:"statuses,omitempty"`
	}{Statuses: opts.Statuses}
	for _, index := range opts.Indices {
		body.IDs = append(body.IDs, fmt.Sprintf("%d", index))
	}
	for _, key := range opts.PubKeys {
		body.IDs = append(body.IDs, key.String())
	}
	data, err := json.Marshal(body)
	if err != nil {
		return nil, errors.Join(errors.New("failed to marshal builders request"), err)
	}
	endpoint := fmt.Sprintf("/eth/v1/beacon/states/%s/builders", opts.State)
	resp, err := s.post(ctx, endpoint, "", &opts.Common, bytes.NewReader(data), ContentTypeJSON, nil)
	if err != nil {
		return nil, errors.Join(errors.New("failed to request builders"), err)
	}
	builders, metadata, err := decodeJSONResponse(bytes.NewReader(resp.body), []*apiv1.Builder{})
	if err != nil {
		return nil, err
	}

	return &api.Response[[]*apiv1.Builder]{Data: builders, Metadata: metadata}, nil
}
