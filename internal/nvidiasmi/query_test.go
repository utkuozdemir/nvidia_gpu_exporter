package nvidiasmi_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/utkuozdemir/nvidia_gpu_exporter/internal/nvidiasmi"
)

// TestQueryTimeoutBoundsUncooperativeCommand proves a wedged command cannot
// hold a collection past its deadline: the context kills the shell, and the
// wait delay unblocks the run even though a surviving child still holds the
// stdout pipe open.
func TestQueryTimeoutBoundsUncooperativeCommand(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	t.Cleanup(cancel)

	start := time.Now()

	_, _, err := nvidiasmi.Query(
		ctx,
		"sh testdata/uncooperative.sh",
		[]nvidiasmi.QField{"uuid"},
		nvidiasmi.DefaultRunFunc,
	)

	elapsed := time.Since(start)

	require.Error(t, err)
	// well under the 30s the child sleeps: the 100ms deadline plus the 2s wait delay
	assert.Less(t, elapsed, 15*time.Second)
}
