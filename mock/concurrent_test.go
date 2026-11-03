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
	"sync"
	"testing"

	"github.com/attestantio/go-eth2-client/mock"
	"github.com/stretchr/testify/require"
)

func TestConcurrentNewAndCancel(t *testing.T) {
	var wg sync.WaitGroup
	errors := make(chan error, 160)
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 20 {
				ctx, cancel := context.WithCancel(context.Background())
				_, err := mock.New(ctx)
				errors <- err
				cancel()
			}
		}()
	}
	wg.Wait()
	close(errors)
	for err := range errors {
		require.NoError(t, err)
	}
}
