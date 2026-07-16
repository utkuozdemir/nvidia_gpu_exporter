package integration_test

import (
	"context"
	"os/exec"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestExporterDoesNotEmbedCaptures guards the shipped binary against ever
// depending on the embedded capture corpus: the corpus is test/development
// data, and importing it would silently grow the release binary by the whole
// corpus size.
func TestExporterDoesNotEmbedCaptures(t *testing.T) {
	t.Parallel()

	cmd := exec.CommandContext(context.Background(),
		"go", "list", "-deps", "../../cmd/nvidia_gpu_exporter")

	output, err := cmd.CombinedOutput()
	require.NoError(t, err, "go list failed: %s", output)

	assert.NotContains(t, string(output), "internal/captures",
		"the exporter binary must not import the capture corpus")
	assert.Contains(t, string(output), "internal/demodata",
		"the demo backend's curated captures are the one embedded corpus the binary may carry")
}
