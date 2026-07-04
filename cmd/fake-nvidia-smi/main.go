// fake-nvidia-smi replays a GPU capture file from testdata/captures, so the
// exporter can run end to end on a machine without a GPU. Used by the
// integration tests, and handy for local development:
//
//	go run ./cmd/fake-nvidia-smi --help-query-gpu
//	nvidia_gpu_exporter --nvidia-smi-command "fake-nvidia-smi --capture <path> --state load"
package main

import (
	"os"

	"github.com/utkuozdemir/nvidia_gpu_exporter/internal/fakesmi"
)

func main() {
	os.Exit(fakesmi.Run(os.Args[1:], os.Stdout, os.Stderr))
}
