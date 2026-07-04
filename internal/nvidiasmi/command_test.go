package nvidiasmi_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/utkuozdemir/nvidia_gpu_exporter/internal/nvidiasmi"
)

//nolint:funlen
func TestSplitCommand(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name     string
		command  string
		expected []string
	}{
		{
			name:     "plain command",
			command:  "nvidia-smi",
			expected: []string{"nvidia-smi"},
		},
		{
			name:     "command with a wrapper",
			command:  "sudo nvidia-smi",
			expected: []string{"sudo", "nvidia-smi"},
		},
		{
			name:     "extra whitespace between words",
			command:  "  sudo   nvidia-smi\t",
			expected: []string{"sudo", "nvidia-smi"},
		},
		{
			name:    "ssh wrapper with options",
			command: "ssh -o StrictHostKeyChecking=no user@host nvidia-smi",
			expected: []string{
				"ssh", "-o", "StrictHostKeyChecking=no", "user@host", "nvidia-smi",
			},
		},
		{
			name:     "unix absolute path",
			command:  "/usr/bin/nvidia-smi",
			expected: []string{"/usr/bin/nvidia-smi"},
		},
		{
			// the backward-compatibility contract: without quotes, backslashes
			// must never be treated as escapes, or existing Windows setups break.
			name:     "unquoted windows path keeps its backslashes",
			command:  `C:\nvidia\nvidia-smi.exe`,
			expected: []string{`C:\nvidia\nvidia-smi.exe`},
		},
		{
			name:     "double-quoted windows path with spaces",
			command:  `"C:\Program Files\NVIDIA Corporation\NVSMI\nvidia-smi.exe"`,
			expected: []string{`C:\Program Files\NVIDIA Corporation\NVSMI\nvidia-smi.exe`},
		},
		{
			name:    "double-quoted windows path with a trailing argument",
			command: `"C:\Program Files\NVIDIA Corporation\NVSMI\nvidia-smi.exe" -someflag`,
			expected: []string{
				`C:\Program Files\NVIDIA Corporation\NVSMI\nvidia-smi.exe`, "-someflag",
			},
		},
		{
			name:     "single-quoted unix path with spaces",
			command:  `'/opt/my tools/nvidia-smi' --someflag`,
			expected: []string{"/opt/my tools/nvidia-smi", "--someflag"},
		},
		{
			name:     "double-quoted unix path with spaces",
			command:  `"/opt/my tools/nvidia-smi"`,
			expected: []string{"/opt/my tools/nvidia-smi"},
		},
		{
			name:     "wrapper followed by a quoted path",
			command:  `sudo "/opt/my tools/nvidia-smi"`,
			expected: []string{"sudo", "/opt/my tools/nvidia-smi"},
		},
		{
			name:     "quote glued to unquoted text",
			command:  `/opt/'my tools'/nvidia-smi`,
			expected: []string{"/opt/my tools/nvidia-smi"},
		},
		{
			// no expansion of any kind happens, quoted or not
			name:     "dollar sign stays literal",
			command:  `"$HOME/nvidia-smi"`,
			expected: []string{"$HOME/nvidia-smi"},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			parts, err := nvidiasmi.SplitCommand(test.command)
			require.NoError(t, err)
			assert.Equal(t, test.expected, parts)
		})
	}
}

func TestSplitCommandErrors(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name    string
		command string
	}{
		{
			name:    "empty",
			command: "",
		},
		{
			name:    "whitespace only",
			command: "   \t ",
		},
		{
			name:    "unbalanced double quote",
			command: `"C:\Program Files\nvidia-smi.exe`,
		},
		{
			name:    "unbalanced single quote",
			command: "'/opt/nvidia-smi",
		},
		{
			name:    "quotes around nothing",
			command: `""`,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			_, err := nvidiasmi.SplitCommand(test.command)
			require.Error(t, err)
		})
	}
}
