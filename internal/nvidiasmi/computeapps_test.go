package nvidiasmi_test

import (
	"os/exec"
	"testing"

	"github.com/neilotoole/slogt/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/utkuozdemir/nvidia_gpu_exporter/internal/nvidiasmi"
)

const computeAppsHeader = "gpu_uuid, pid, process_name, used_gpu_memory [MiB]"

func TestParseComputeApps(t *testing.T) {
	t.Parallel()

	// from the H200 capture: normal rows, including names with quotes, unicode
	// and the "?" a modern driver substitutes for a comma
	output := computeAppsHeader + "\n" +
		"GPU-ae3738d5-9aa2-e1c9-53be-65b6d373d3d9, 10525, /root/tools/memhog, 1542 MiB\n" +
		"GPU-ae3738d5-9aa2-e1c9-53be-65b6d373d3d9, 10529, /root/tools/memhög ünïcode, 818 MiB\n" +
		"GPU-ae3738d5-9aa2-e1c9-53be-65b6d373d3d9, 10526, /root/tools/mem? hog, 818 MiB\n" +
		`GPU-ae3738d5-9aa2-e1c9-53be-65b6d373d3d9, 10528, /root/tools/mem "q" hog, 818 MiB` + "\n" +
		"GPU-ae3738d5-9aa2-e1c9-53be-65b6d373d3d9, 10550, ./gpu_burn, 14410 MiB"

	apps, err := nvidiasmi.ParseComputeApps(output, slogt.New(t))

	require.NoError(t, err)
	require.Len(t, apps, 5)

	assert.Equal(t, nvidiasmi.ComputeApp{
		GPUUUID:     "ae3738d5-9aa2-e1c9-53be-65b6d373d3d9",
		PID:         "10525",
		ProcessName: "/root/tools/memhog",
		UsedMemory:  "1542 MiB",
	}, apps[0])
	assert.Equal(t, "/root/tools/memhög ünïcode", apps[1].ProcessName)
	assert.Equal(t, "/root/tools/mem? hog", apps[2].ProcessName)
	assert.Equal(t, `/root/tools/mem "q" hog`, apps[3].ProcessName)
	assert.Equal(t, "./gpu_burn", apps[4].ProcessName)
}

func TestParseComputeAppsEmpty(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		output string
	}{
		{name: "header only", output: computeAppsHeader + "\n"},
		{name: "blank output", output: "\n\n"},
		{name: "empty output", output: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			apps, err := nvidiasmi.ParseComputeApps(tt.output, slogt.New(t))

			require.NoError(t, err)
			assert.Empty(t, apps)
		})
	}
}

func TestParseComputeAppsWDDMNAMemory(t *testing.T) {
	t.Parallel()

	// from the Windows WDDM captures: memory reads [N/A] but the row is valid
	output := computeAppsHeader + "\n" +
		`GPU-00000000-0000-0000-0000-000000000000, 2056, C:\Windows\System32\dwm.exe, [N/A]`

	apps, err := nvidiasmi.ParseComputeApps(output, slogt.New(t))

	require.NoError(t, err)
	require.Len(t, apps, 1)
	assert.Equal(t, "2056", apps[0].PID)
	assert.Equal(t, `C:\Windows\System32\dwm.exe`, apps[0].ProcessName)
	assert.Equal(t, "[N/A]", apps[0].UsedMemory)
}

func TestParseComputeAppsCommaInName(t *testing.T) {
	t.Parallel()

	// defense for drivers that do not sanitize commas: the name is everything
	// between pid and memory, rejoined
	output := computeAppsHeader + "\n" +
		"GPU-abc, 42, /root/tools/mem, hog, 818 MiB\n" +
		"GPU-abc, 43, /a, b, c, d, 12 MiB"

	apps, err := nvidiasmi.ParseComputeApps(output, slogt.New(t))

	require.NoError(t, err)
	require.Len(t, apps, 2)
	assert.Equal(t, "/root/tools/mem, hog", apps[0].ProcessName)
	assert.Equal(t, "/a, b, c, d", apps[1].ProcessName)
	assert.Equal(t, "12 MiB", apps[1].UsedMemory)
}

func TestParseComputeAppsSkipsUnusableRows(t *testing.T) {
	t.Parallel()

	// from the MIG-in-container capture: a fully unreadable row, plus a
	// malformed short row; both are skipped and the surrounding rows survive
	unreadableRow := "GPU-ae3738d5-9aa2-e1c9-53be-65b6d373d3d9, [Insufficient Permissions], " +
		"[Insufficient Permissions], [Insufficient Permissions]"
	output := computeAppsHeader + "\n" +
		"GPU-abc, 41, ./before, 10 MiB\n" +
		unreadableRow + "\n" +
		"GPU-abc, 42\n" +
		"GPU-abc, not-a-pid, ./bogus, 5 MiB\n" +
		"GPU-abc, 43, ./after, 20 MiB"

	apps, err := nvidiasmi.ParseComputeApps(output, slogt.New(t))

	require.NoError(t, err)
	require.Len(t, apps, 2)
	assert.Equal(t, "./before", apps[0].ProcessName)
	assert.Equal(t, "./after", apps[1].ProcessName)
}

func TestParseComputeAppsHeaderMismatch(t *testing.T) {
	t.Parallel()

	_, err := nvidiasmi.ParseComputeApps("pid, process_name\n1, foo", slogt.New(t))

	require.ErrorContains(t, err, "unexpected compute apps header")
}

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

func TestQueryComputeApps(t *testing.T) {
	t.Parallel()

	output := computeAppsHeader + "\n" +
		"GPU-abc, 42, ./gpu_burn, 14410 MiB\n"

	apps, err := nvidiasmi.QueryComputeApps(t.Context(), "nvsmi", func(cmd *exec.Cmd) error {
		assert.Contains(t, cmd.Args, "--query-compute-apps=gpu_uuid,pid,process_name,used_gpu_memory")
		assert.Contains(t, cmd.Args, "--format=csv")

		_, _ = cmd.Stdout.Write([]byte(output))

		return nil
	}, slogt.New(t))

	require.NoError(t, err)
	require.Len(t, apps, 1)
	assert.Equal(t, "abc", apps[0].GPUUUID)
	assert.Equal(t, "./gpu_burn", apps[0].ProcessName)
}

func TestQueryComputeAppsCommandFailure(t *testing.T) {
	t.Parallel()

	_, err := nvidiasmi.QueryComputeApps(
		t.Context(),
		"definitely-not-nvidia-smi-xyz",
		nvidiasmi.DefaultRunFunc,
		slogt.New(t),
	)

	require.Error(t, err)
}
