package nvidiasmi_test

import (
	"errors"
	"os/exec"
	"strings"
	"testing"

	"github.com/neilotoole/slogt/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/utkuozdemir/nvidia_gpu_exporter/internal/nvidiasmi"
)

func testTable(rows ...map[nvidiasmi.QField]string) *nvidiasmi.Table {
	out := &nvidiasmi.Table{Rows: make([]nvidiasmi.Row, 0, len(rows))}

	for _, cells := range rows {
		mapped := make(map[nvidiasmi.QField]nvidiasmi.Cell, len(cells))
		for k, v := range cells {
			mapped[k] = nvidiasmi.Cell{RawValue: v}
		}

		out.Rows = append(out.Rows, nvidiasmi.Row{QFieldToCells: mapped})
	}

	return out
}

func TestParseProcessesTypesAndMIG(t *testing.T) {
	t.Parallel()

	// Exercises 4-digit vs 8-digit PCI domain matching, C / G / C+G / M+C
	// process types, MIG GI/CI attribution, and empty Name fallback.
	table := testTable(
		map[nvidiasmi.QField]string{
			nvidiasmi.UUIDQField: "GPU-aaaa",
			"pci.bus_id":         "00000000:83:00.0",
		},
	)

	output := `==============NVSMI LOG==============

Attached GPUs                             : 1
GPU 0000:83:00.0
    Processes
        GPU instance ID                   : 2
        Compute instance ID               : 0
        Process ID                        : 101
            Type                          : C
            Name                          : /opt/vllm/bin/python3 --model meta-llama/Llama-3-70B
            Used GPU Memory               : 40960 MiB
        Process ID                        : 102
            Type                          : M+C
            Name                          : /usr/bin/nvidia-cuda-mps-server
            Used GPU Memory               : 512 MiB
        GPU instance ID                   : N/A
        Compute instance ID               : N/A
        Process ID                        : 103
            Type                          : C+G
            Name                          :
            Used GPU Memory               : 256 MiB
`

	procs, err := nvidiasmi.ParseProcesses(output, table, slogt.New(t))
	require.NoError(t, err)

	require.Len(t, procs.Compute, 3)
	assert.Equal(t, nvidiasmi.ComputeApp{
		GPUUUID:           "aaaa",
		PID:               "101",
		ProcessName:       "/opt/vllm/bin/python3 --model meta-llama/Llama-3-70B",
		UsedMemory:        "40960 MiB",
		GPUInstanceID:     "2",
		ComputeInstanceID: "0",
	}, procs.Compute[0])
	assert.Equal(t, "102", procs.Compute[1].PID)
	assert.Empty(t, procs.Compute[1].GPUInstanceID)
	assert.Equal(t, "[Not Found]", procs.Compute[2].ProcessName)

	require.Len(t, procs.Graphics, 1)
	assert.Equal(t, "103", procs.Graphics[0].PID)
	assert.Equal(t, "[Not Found]", procs.Graphics[0].ProcessName)
}

func TestParseProcessesWDDMMemoryUnavailable(t *testing.T) {
	t.Parallel()

	table := testTable(
		map[nvidiasmi.QField]string{
			nvidiasmi.UUIDQField: "GPU-bbbb",
			"pci.bus_id":         "00000000:08:00.0",
		},
	)

	output := `==============NVSMI LOG==============

Attached GPUs                             : 1
GPU 00000000:08:00.0
    Processes
        Process ID                        : 201
            Type                          : G
            Name                          : C:\Windows\System32\dwm.exe
            Used GPU Memory               : Not available in WDDM driver model
        Process ID                        : 202
            Type                          : M+C+G
            Name                          : C:\Program Files\App\worker.exe
            Used GPU Memory               : 1024 MiB
`

	procs, err := nvidiasmi.ParseProcesses(output, table, slogt.New(t))
	require.NoError(t, err)

	require.Len(t, procs.Compute, 1)
	assert.Equal(t, "bbbb", procs.Compute[0].GPUUUID)
	assert.Equal(t, "202", procs.Compute[0].PID)

	require.Len(t, procs.Graphics, 2)
	assert.Equal(t, "201", procs.Graphics[0].PID)
	assert.Equal(t, "[N/A]", procs.Graphics[0].UsedMemory)
	assert.Equal(t, "202", procs.Graphics[1].PID)
}

func TestParseProcessesRejectsUnexpectedOutput(t *testing.T) {
	t.Parallel()

	_, err := nvidiasmi.ParseProcesses("completely unrelated text", nil, slogt.New(t))
	require.ErrorContains(t, err, "unexpected nvidia-smi -q -d PIDS output")
}

func TestParsePmonVariants(t *testing.T) {
	t.Parallel()

	indexToUUID := map[string]string{"0": "gpu-0", "1": "gpu-1"}

	t.Run("R550 10-column with spaces in command and dash engines", func(t *testing.T) {
		t.Parallel()

		out := `# gpu         pid   type     sm    mem    enc    dec    jpg    ofa    command
# Idx           #    C/G      %      %      %      %      %      %    name
    0        2056     G       2      5      -      0     12      3    C:\Program Files\NVIDIA Share.exe
    1        9999     C       -      -      -      -      -      -    llama-server
    2        8888     C       -      -      -      -      -      -    gpu_burn
`
		got := nvidiasmi.ParsePmon(out, indexToUUID)
		require.Len(t, got, 2, "unmapped MIG GPU 2 row must stay absent")

		util := got[nvidiasmi.PmonKey{GPUUUID: "gpu-0", PID: "2056"}]
		require.NotNil(t, util)
		assert.InDelta(t, 0.02, *util.SMRatio, 1e-9)
		assert.InDelta(t, 0.05, *util.MemoryRatio, 1e-9)
		assert.InDelta(t, 0.0, *util.EncoderRatio, 1e-9)
		assert.InDelta(t, 0.0, *util.DecoderRatio, 1e-9)
		assert.InDelta(t, 0.12, *util.JPEGRatio, 1e-9)
		assert.InDelta(t, 0.03, *util.OFARatio, 1e-9)

		idle := got[nvidiasmi.PmonKey{GPUUUID: "gpu-1", PID: "9999"}]
		require.NotNil(t, idle)
		assert.InDelta(t, 0.0, *idle.SMRatio, 1e-9)
		assert.InDelta(t, 0.0, *idle.MemoryRatio, 1e-9)
		assert.InDelta(t, 0.0, *idle.EncoderRatio, 1e-9)
		assert.InDelta(t, 0.0, *idle.DecoderRatio, 1e-9)
		assert.InDelta(t, 0.0, *idle.JPEGRatio, 1e-9)
		assert.InDelta(t, 0.0, *idle.OFARatio, 1e-9)
	})

	t.Run("pre-R550 8-column headerless output", func(t *testing.T) {
		t.Parallel()

		out := "    0      10550     C      98     44      0      -    ./gpu_burn\n"
		got := nvidiasmi.ParsePmon(out, indexToUUID)

		util := got[nvidiasmi.PmonKey{GPUUUID: "gpu-0", PID: "10550"}]
		require.NotNil(t, util)
		assert.InDelta(t, 0.98, *util.SMRatio, 1e-9)
		assert.InDelta(t, 0.44, *util.MemoryRatio, 1e-9)
		assert.InDelta(t, 0.0, *util.EncoderRatio, 1e-9)
		assert.InDelta(t, 0.0, *util.DecoderRatio, 1e-9)
		assert.Nil(t, util.JPEGRatio)
		assert.Nil(t, util.OFARatio)
	})
}

func TestAttachPmonUtilizationSkipsMIG(t *testing.T) {
	t.Parallel()

	migTable := testTable(
		map[nvidiasmi.QField]string{
			nvidiasmi.UUIDQField: "GPU-mig0",
			"index":              "0",
			"mig.mode.current":   "Enabled",
		},
	)

	called := false
	neverRun := func(*exec.Cmd) error {
		called = true

		return nil
	}

	// Pure MIG host via mig.mode.current skips spawning pmon completely.
	migApps := []nvidiasmi.ComputeApp{{GPUUUID: "mig0", PID: "10"}}
	nvidiasmi.AttachPmonUtilization(t.Context(), "nvidia-smi", migTable, migApps, nil, neverRun, slogt.New(t))
	assert.False(t, called)
	assert.Nil(t, migApps[0].Utilization)

	// Explicit --gpu-query-field-names without mig.mode.current still skips MIG
	// apps via GPUInstanceID.
	noMIGFieldTable := testTable(
		map[nvidiasmi.QField]string{nvidiasmi.UUIDQField: "GPU-mig0", "index": "0"},
	)
	giApps := []nvidiasmi.ComputeApp{{GPUUUID: "mig0", PID: "10", GPUInstanceID: "2"}}
	nvidiasmi.AttachPmonUtilization(t.Context(), "nvidia-smi", noMIGFieldTable, giApps, nil, neverRun, slogt.New(t))
	assert.False(t, called)
	assert.Nil(t, giApps[0].Utilization)
}

func TestAttachPmonUtilizationChunkingAndSoftFailure(t *testing.T) {
	t.Parallel()

	table := testTable(
		map[nvidiasmi.QField]string{nvidiasmi.UUIDQField: "GPU-u0", "index": "0"},
		map[nvidiasmi.QField]string{nvidiasmi.UUIDQField: "GPU-u1", "index": "1"},
		map[nvidiasmi.QField]string{nvidiasmi.UUIDQField: "GPU-u2", "index": "2"},
		map[nvidiasmi.QField]string{nvidiasmi.UUIDQField: "GPU-u3", "index": "3"},
		map[nvidiasmi.QField]string{nvidiasmi.UUIDQField: "GPU-u4", "index": "4"},
	)

	compute := []nvidiasmi.ComputeApp{
		{GPUUUID: "u0", PID: "10"},
		{GPUUUID: "u1", PID: "11"},
		{GPUUUID: "u2", PID: "12"},
		{GPUUUID: "u3", PID: "13"},
		{GPUUUID: "u4", PID: "14"},
	}

	var calls []string

	run := func(cmd *exec.Cmd) error {
		calls = append(calls, strings.Join(cmd.Args[1:], " "))

		if strings.HasSuffix(calls[len(calls)-1], "0,1,2,3") {
			_, _ = cmd.Stdout.Write([]byte(
				"# gpu pid type sm mem enc dec jpg ofa command\n" +
					"0 10 C 50 10 0 0 0 0 a\n",
			))

			return nil
		}

		_, _ = cmd.Stdout.Write([]byte(
			"# gpu pid type sm mem enc dec jpg ofa command\n" +
				"4 14 C 80 20 0 0 5 1 b\n",
		))

		return nil
	}

	nvidiasmi.AttachPmonUtilization(t.Context(), "nvidia-smi", table, compute, nil, run, slogt.New(t))

	assert.Equal(t, []string{"pmon -c 1 -i 0,1,2,3", "pmon -c 1 -i 4"}, calls)
	require.NotNil(t, compute[0].Utilization)
	assert.InDelta(t, 0.50, *compute[0].Utilization.SMRatio, 1e-9)
	require.NotNil(t, compute[4].Utilization)
	assert.InDelta(t, 0.80, *compute[4].Utilization.SMRatio, 1e-9)
	assert.InDelta(t, 0.05, *compute[4].Utilization.JPEGRatio, 1e-9)

	// Soft failure leaves Utilization nil without failing the caller.
	failing := []nvidiasmi.ComputeApp{{GPUUUID: "u0", PID: "10"}}
	nvidiasmi.AttachPmonUtilization(
		t.Context(),
		"nvidia-smi",
		table,
		failing,
		nil,
		func(*exec.Cmd) error { return errors.New("pmon unsupported") },
		slogt.New(t),
	)
	assert.Nil(t, failing[0].Utilization)
}

func TestAttachPmonUtilizationPartialBatchFailure(t *testing.T) {
	t.Parallel()

	table := testTable(
		map[nvidiasmi.QField]string{nvidiasmi.UUIDQField: "GPU-u0", "index": "0"},
		map[nvidiasmi.QField]string{nvidiasmi.UUIDQField: "GPU-u1", "index": "1"},
		map[nvidiasmi.QField]string{nvidiasmi.UUIDQField: "GPU-u2", "index": "2"},
		map[nvidiasmi.QField]string{nvidiasmi.UUIDQField: "GPU-u3", "index": "3"},
		map[nvidiasmi.QField]string{nvidiasmi.UUIDQField: "GPU-u4", "index": "4"},
	)

	compute := []nvidiasmi.ComputeApp{
		{GPUUUID: "u0", PID: "10"},
		{GPUUUID: "u1", PID: "11"},
		{GPUUUID: "u2", PID: "12"},
		{GPUUUID: "u3", PID: "13"},
		{GPUUUID: "u4", PID: "14"},
	}

	run := func(cmd *exec.Cmd) error {
		if strings.HasSuffix(strings.Join(cmd.Args[1:], " "), "0,1,2,3") {
			_, _ = cmd.Stdout.Write([]byte("# gpu pid type sm mem enc dec command\n0 10 C 42 5 0 0 a\n"))

			return nil
		}

		return errors.New("gpu 4 reset")
	}

	nvidiasmi.AttachPmonUtilization(t.Context(), "nvidia-smi", table, compute, nil, run, slogt.New(t))

	require.NotNil(t, compute[0].Utilization)
	assert.InDelta(t, 0.42, *compute[0].Utilization.SMRatio, 1e-9)
	assert.Nil(t, compute[4].Utilization)
}
