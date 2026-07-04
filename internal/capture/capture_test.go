package capture_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/utkuozdemir/nvidia_gpu_exporter/internal/capture"
)

// TestLoadCorpus parses every committed capture and checks the invariants the
// rest of the codebase relies on. New captures are picked up automatically.
func TestLoadCorpus(t *testing.T) {
	t.Parallel()

	paths, err := filepath.Glob(filepath.Join("..", "..", "testdata", "captures", "*.txt"))
	require.NoError(t, err)
	require.NotEmpty(t, paths)

	for _, path := range paths {
		t.Run(filepath.Base(path), func(t *testing.T) {
			t.Parallel()

			capt, err := capture.Load(path)
			require.NoError(t, err)

			assert.Contains(t, capt.Header, "collected_at:")
			assert.NotEmpty(t, capt.Sections)

			commandless := 0

			for _, section := range capt.Sections {
				assert.Contains(t, []string{"capabilities", "idle", "load"}, section.State)
				assert.NotEmpty(t, section.Label)

				if section.Command == "" {
					commandless++
				} else {
					assert.True(t, strings.HasPrefix(section.Command, "nvidia-smi"),
						"command %q does not invoke nvidia-smi", section.Command)
				}
			}

			// the derived query-gpu field list is the only section no single
			// command produced
			assert.Equal(t, 1, commandless)

			helpSection := capt.Find("capabilities", "help-query-gpu")
			require.NotNil(t, helpSection)
			assert.Equal(t, "nvidia-smi --help-query-gpu", helpSection.Command)
			assert.NotEmpty(t, helpSection.Body)

			gpuSection := capt.Find("idle", "query-gpu (csv")
			require.NotNil(t, gpuSection)
			assert.True(t, strings.HasPrefix(gpuSection.Command, "nvidia-smi --query-gpu="))
			assert.True(t, strings.HasSuffix(gpuSection.Command, "--format=csv"))
			require.NotEmpty(t, gpuSection.Body)
			assert.False(t, strings.HasPrefix(gpuSection.Body, "\n"))
			assert.False(t, strings.HasSuffix(gpuSection.Body, "\n"))

			appsSection := capt.Find("idle", "query-compute-apps")
			require.NotNil(t, appsSection)
			assert.Contains(t, appsSection.Command, "--query-compute-apps=")
		})
	}
}

func TestParseErrors(t *testing.T) {
	t.Parallel()

	fence := strings.Repeat("#", 80)

	for _, test := range []struct {
		name    string
		content string
	}{
		{
			name:    "empty",
			content: "",
		},
		{
			name:    "content outside any block",
			content: "loose text\n",
		},
		{
			name:    "unterminated heading",
			content: fence + "\n# idle :: something\n",
		},
		{
			name:    "heading line without comment prefix",
			content: fence + "\n# idle :: something\nnot a comment\n" + fence + "\nbody\n",
		},
		{
			name:    "header only, no sections",
			content: fence + "\n# just a header\n" + fence + "\nmetadata: yes\n",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			_, err := capture.Parse(test.content)
			require.Error(t, err)
		})
	}
}

func TestParseSection(t *testing.T) {
	t.Parallel()

	fence := strings.Repeat("#", 80)
	content := fence + "\n" +
		"# my header\n" +
		fence + "\n" +
		"collected_at: whenever\n" +
		"\n\n" +
		fence + "\n" +
		"# idle :: query-gpu (csv, what the exporter parses)\n" +
		"# $ nvidia-smi --query-gpu=uuid,name --format=csv \n" +
		fence + "\n" +
		"\n" +
		"uuid, name\n" +
		"gpu-0, some gpu\n" +
		"\n\n" +
		fence + "\n" +
		"# capabilities :: query-gpu field list (derived, used for query-gpu above)\n" +
		fence + "\n" +
		"uuid\nname\n"

	capt, err := capture.Parse(content)
	require.NoError(t, err)

	assert.Equal(t, "collected_at: whenever", capt.Header)
	require.Len(t, capt.Sections, 2)

	section := capt.Find("idle", "query-gpu (csv")
	require.NotNil(t, section)
	assert.Equal(t, "idle", section.State)
	assert.Equal(t, "query-gpu (csv, what the exporter parses)", section.Label)
	assert.Equal(t, "nvidia-smi --query-gpu=uuid,name --format=csv", section.Command)
	assert.Equal(t, "uuid, name\ngpu-0, some gpu", section.Body)

	derived := capt.Find("capabilities", "query-gpu field list")
	require.NotNil(t, derived)
	assert.Empty(t, derived.Command)
	assert.Equal(t, "uuid\nname", derived.Body)

	assert.Nil(t, capt.Find("load", "query-gpu (csv"))
	assert.Nil(t, capt.Find("idle", "nonexistent"))
}
