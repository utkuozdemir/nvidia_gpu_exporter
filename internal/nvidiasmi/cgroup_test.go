package nvidiasmi_test

import (
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/assert"

	"github.com/utkuozdemir/nvidia_gpu_exporter/internal/nvidiasmi"
)

//nolint:funlen // table-driven cgroup layout matrix
func TestParseCgroupIdentity(t *testing.T) {
	t.Parallel()

	const (
		cid1 = "ad46282edd65b1c223e5175af1d71cca1dee615a517cbfcfaab9a1c90db0495b"
		cid2 = "26f38348295a73847d9fc30bbb574101d19d0b45779879828c6a2e557d741ce5"
	)

	tests := []struct {
		name            string
		content         string
		wantContainerID string
		wantSystemdUnit string
	}{
		{
			name:            "empty",
			content:         "",
			wantContainerID: "",
			wantSystemdUnit: "",
		},
		{
			name:            "cgroup v2 root",
			content:         "0::/\n",
			wantContainerID: "",
			wantSystemdUnit: "",
		},
		{
			name:            "cgroup v2 init scope",
			content:         "0::/init.scope\n",
			wantContainerID: "",
			wantSystemdUnit: "",
		},
		{
			name:            "cgroup v2 docker scope",
			content:         "0::/system.slice/docker-" + cid1 + ".scope\n",
			wantContainerID: cid1,
			wantSystemdUnit: "",
		},
		{
			name:            "cgroup v2 docker scope with init subcgroup",
			content:         "0::/system.slice/docker-" + cid1 + ".scope/init\n",
			wantContainerID: cid1,
			wantSystemdUnit: "",
		},
		{
			name:            "cgroup v2 private cgroupns sibling docker scope",
			content:         "0::/../docker-" + cid1 + ".scope\n",
			wantContainerID: cid1,
			wantSystemdUnit: "",
		},
		{
			name: "cgroup v2 kubernetes cri-containerd scope from private cgroupns",
			content: "0::/../../kubepods.slice/kubepods-burstable.slice/" +
				"kubepods-burstable-pod12345678_1234_5678_90ab_cdef12345678.slice/" +
				"cri-containerd-" + cid1 + ".scope\n",
			wantContainerID: cid1,
			wantSystemdUnit: "",
		},
		{
			name: "cgroup v2 kubernetes crio scope",
			content: "0::/kubepods.slice/kubepods-besteffort.slice/" +
				"kubepods-besteffort-pod12345678_1234_5678_90ab_cdef12345678.slice/" +
				"crio-" + cid1 + ".scope\n",
			wantContainerID: cid1,
			wantSystemdUnit: "",
		},
		{
			name:            "cgroup v2 crio conmon scope is ignored",
			content:         "0::/kubepods.slice/crio-conmon-" + cid1 + ".scope\n",
			wantContainerID: "",
			wantSystemdUnit: "",
		},
		{
			name: "cgroup v2 rootless podman scope under user@1000.service",
			content: "0::/user.slice/user-1000.slice/user@1000.service/user.slice/" +
				"libpod-" + cid2 + ".scope/container\n",
			wantContainerID: cid2,
			wantSystemdUnit: "",
		},
		{
			name: "cgroup v2 podman quadlet split cgroup under service",
			content: "0::/user.slice/user-1000.slice/user@1000.service/app.slice/" +
				"vllm.service/libpod-payload-" + cid2 + "\n",
			wantContainerID: cid2,
			wantSystemdUnit: "",
		},
		{
			name:            "cgroup v2 cgroupfs docker under docker.service",
			content:         "0::/system.slice/docker.service/" + cid1 + "\n",
			wantContainerID: cid1,
			wantSystemdUnit: "",
		},
		{
			name:            "cgroup v2 cgroupfs kubepods bare container id",
			content:         "0::/kubepods/burstable/pod12345678-1234-5678-90ab-cdef12345678/" + cid1 + "\n",
			wantContainerID: cid1,
			wantSystemdUnit: "",
		},
		{
			name:            "cgroup v2 systemd service",
			content:         "0::/system.slice/ollama.service\n",
			wantContainerID: "",
			wantSystemdUnit: "ollama.service",
		},
		{
			name:            "cgroup v2 systemd template service from private cgroupns",
			content:         "0::/../../vllm@0.service\n",
			wantContainerID: "",
			wantSystemdUnit: "vllm@0.service",
		},
		{
			name:            "cgroup v2 systemd service with delegated subcgroup",
			content:         "0::/system.slice/ollama.service/worker\n",
			wantContainerID: "",
			wantSystemdUnit: "ollama.service",
		},
		{
			name:            "cgroup v2 systemd user service under user@1000.service",
			content:         "0::/user.slice/user-1000.slice/user@1000.service/app.slice/ollama.service\n",
			wantContainerID: "",
			wantSystemdUnit: "ollama.service",
		},
		{
			name:            "cgroup v2 desktop app scope under user@1000.service is empty",
			content:         "0::/user.slice/user-1000.slice/user@1000.service/app.slice/app-org.kde.konsole-1234.scope\n",
			wantContainerID: "",
			wantSystemdUnit: "",
		},
		{
			name:            "cgroup v2 user manager init.scope is empty",
			content:         "0::/user.slice/user-1000.slice/user@1000.service/init.scope\n",
			wantContainerID: "",
			wantSystemdUnit: "",
		},
		{
			name:            "cgroup v2 bare user@1000.service is empty",
			content:         "0::/user.slice/user-1000.slice/user@1000.service\n",
			wantContainerID: "",
			wantSystemdUnit: "",
		},
		{
			name:            "cgroup v2 login session scope is empty",
			content:         "0::/user.slice/user-1000.slice/session-18.scope\n",
			wantContainerID: "",
			wantSystemdUnit: "",
		},
		{
			name: "cgroup v1 multi-line container",
			content: "12:memory:/kubepods.slice/kubepods-burstable.slice/cri-containerd-" + cid1 + ".scope\n" +
				"1:name=systemd:/kubepods.slice/kubepods-burstable.slice/cri-containerd-" + cid1 + ".scope\n" +
				"0::/\n",
			wantContainerID: cid1,
			wantSystemdUnit: "",
		},
		{
			name: "cgroup v1 multi-line systemd service",
			content: "12:memory:/system.slice/vllm.service\n" +
				"1:name=systemd:/system.slice/vllm.service\n" +
				"0::/\n",
			wantContainerID: "",
			wantSystemdUnit: "vllm.service",
		},
		{
			name:            "uppercase hex is not treated as container id",
			content:         "0::/docker/AD46282EDD65B1C223E5175AF1D71CCA1DEE615A517CBFCFAAB9A1C90DB0495B\n",
			wantContainerID: "",
			wantSystemdUnit: "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			gotContainerID, gotUnit := nvidiasmi.ParseCgroupIdentity(tc.content)
			assert.Equal(t, tc.wantContainerID, gotContainerID)
			assert.Equal(t, tc.wantSystemdUnit, gotUnit)
		})
	}
}

func TestReadProcessIdentity(t *testing.T) {
	t.Parallel()

	const cid = "ad46282edd65b1c223e5175af1d71cca1dee615a517cbfcfaab9a1c90db0495b"

	procFS := fstest.MapFS{
		"1001/cgroup": &fstest.MapFile{
			Data: []byte("0::/system.slice/docker-" + cid + ".scope\n"),
		},
		"1002/cgroup": &fstest.MapFile{
			Data: []byte("0::/system.slice/ollama.service\n"),
		},
	}

	gotCID, gotUnit := nvidiasmi.ReadProcessIdentity(procFS, "1001")
	assert.Equal(t, cid, gotCID)
	assert.Empty(t, gotUnit)

	gotCID, gotUnit = nvidiasmi.ReadProcessIdentity(procFS, "1002")
	assert.Empty(t, gotCID)
	assert.Equal(t, "ollama.service", gotUnit)

	gotCID, gotUnit = nvidiasmi.ReadProcessIdentity(procFS, "9999")
	assert.Empty(t, gotCID)
	assert.Empty(t, gotUnit)

	gotCID, gotUnit = nvidiasmi.ReadProcessIdentity(procFS, "../1001")
	assert.Empty(t, gotCID)
	assert.Empty(t, gotUnit)

	gotCID, gotUnit = nvidiasmi.ReadProcessIdentity(nil, "1001")
	assert.Empty(t, gotCID)
	assert.Empty(t, gotUnit)
}
