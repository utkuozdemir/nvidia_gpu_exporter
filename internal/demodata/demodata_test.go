package demodata_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/utkuozdemir/nvidia_gpu_exporter/internal/demodata"
)

// TestExactlyTheCuratedCaptures pins the embedded set: the demo data is a
// deliberate size budget, and a stray capture landing in this directory
// would silently bloat every shipped binary.
func TestExactlyTheCuratedCaptures(t *testing.T) {
	t.Parallel()

	names, err := fs.Glob(demodata.FS, "*.txt")
	require.NoError(t, err)

	assert.ElementsMatch(t, []string{
		"linux-x86_64__nvidia-geforce-rtx-4080-super__595.71.05.txt",
		"linux-x86_64__nvidia-h200__590.48.01.txt",
	}, names)
}

// TestCapturesMatchTheCorpusSources keeps the curated copies byte-identical
// to their internal/captures sources, so the demo never drifts from the
// corpus the rest of the machinery is verified against.
func TestCapturesMatchTheCorpusSources(t *testing.T) {
	t.Parallel()

	names, err := fs.Glob(demodata.FS, "*.txt")
	require.NoError(t, err)

	for _, name := range names {
		embedded, err := fs.ReadFile(demodata.FS, name)
		require.NoError(t, err)

		source, err := os.ReadFile(filepath.Join("..", "captures", name))
		require.NoError(t, err, "every demo capture must come from the corpus")

		assert.Equal(t, source, embedded, "capture %s drifted from its corpus source", name)
	}
}
