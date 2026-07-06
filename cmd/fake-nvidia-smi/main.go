// fake-nvidia-smi replays a GPU capture, so the exporter can run end to end
// on a machine without a GPU. The corpus from internal/captures is embedded,
// making the binary self-contained; --capture takes an embedded capture name
// or a path to a capture file. Repeat --set field=value to pin a field to a
// value, and --set-range field=min:max to serve a fresh random value in that
// range on each run, so metrics move. --config file.yaml carries the whole
// setup (capture, state, per-field overrides, failure injection) instead of
// flags; because the fake is invoked fresh each scrape, editing the file changes
// the next scrape with no exporter restart. Used by the integration tests, and
// handy for local development:
//
//	go run ./cmd/fake-nvidia-smi --help-query-gpu
//	nvidia_gpu_exporter \
//	  --nvidia-smi-command "fake-nvidia-smi --capture linux-x86_64__nvidia-h200__590.48.01 --state load"
package main

import (
	"os"

	"github.com/utkuozdemir/nvidia_gpu_exporter/internal/fakesmi"
)

func main() {
	os.Exit(fakesmi.Run(os.Args[1:], os.Stdout, os.Stderr))
}
