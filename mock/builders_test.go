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

package mock_test

import (
	"context"
	"github.com/attestantio/go-eth2-client/api"
	apiv1 "github.com/attestantio/go-eth2-client/api/v1"
	"github.com/attestantio/go-eth2-client/mock"
	"github.com/stretchr/testify/require"
	"sync"
	"testing"
)

func TestBuilders(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	service, err := mock.New(ctx)
	require.NoError(t, err)
	opts := &api.BuildersOpts{State: "head"}
	empty, err := service.Builders(ctx, opts)
	require.NoError(t, err)
	require.NotNil(t, empty)
	require.Empty(t, empty.Data)
	expected := &api.Response[[]*apiv1.Builder]{Data: []*apiv1.Builder{{}}}
	service.BuildersFunc = func(_ context.Context, got *api.BuildersOpts) (*api.Response[[]*apiv1.Builder], error) {
		require.Equal(t, opts, got)
		return expected, nil
	}
	result, err := service.Builders(ctx, opts)
	require.NoError(t, err)
	require.Same(t, expected, result)
}

func TestConcurrentMockLifecycle(t *testing.T) {
	var wg sync.WaitGroup
	errs := make(chan error, 32)
	for range 32 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx, cancel := context.WithCancel(context.Background())
			_, err := mock.New(ctx)
			errs <- err
			cancel()
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
}
