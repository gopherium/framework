// SPDX-License-Identifier: Apache-2.0

package pluginkit_test

import (
	"slices"
	"testing"

	"github.com/gopherium/framework/pluginkit"
)

func TestHostStopReachesPluginsThatNeverStarted(t *testing.T) {
	t.Parallel()

	var calls []string
	host := pluginkit.NewHost(
		&fakePlugin{id: "alpha", calls: &calls},
		&fakePlugin{id: "beta", calls: &calls},
	)

	if err := host.Stop(t.Context()); err != nil {
		t.Fatalf("Stop() error = %v, want nil", err)
	}

	if want := []string{"beta stop", "alpha stop"}; !slices.Equal(want, calls) {
		t.Errorf("stop calls = %v, want every plugin stopped in reverse order without a start", calls)
	}
}
