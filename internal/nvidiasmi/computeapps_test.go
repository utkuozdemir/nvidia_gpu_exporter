package nvidiasmi_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/utkuozdemir/nvidia_gpu_exporter/internal/nvidiasmi"
)

func TestNormalizeUUID(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "ae3738d5-9aa2", nvidiasmi.NormalizeUUID(" GPU-AE3738D5-9aa2 "))
	assert.Equal(t, "ae3738d5", nvidiasmi.NormalizeUUID("ae3738d5"))
}

func TestIsKnownAbsentValue(t *testing.T) {
	t.Parallel()

	absentValues := []string{
		"[N/A]", "N/A", "[Not Supported]", "[Insufficient Permissions]", "[Unknown Error]", "", " ",
	}
	for _, absent := range absentValues {
		assert.True(t, nvidiasmi.IsKnownAbsentValue(absent), absent)
	}

	for _, present := range []string{"1542 MiB", "0", "[Requested functionality has been deprecated]"} {
		assert.False(t, nvidiasmi.IsKnownAbsentValue(present), present)
	}
}
