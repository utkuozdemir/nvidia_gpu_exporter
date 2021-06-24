package main

import (
	"fmt"
	"github.com/utkuozdemir/nvidia_gpu_exporter/internal/exporter"
	"gopkg.in/alecthomas/kingpin.v2"
	"os"
	"strings"
)

func main() {
	var (
		nvidiaSmiCommand = kingpin.Flag("nvidia-smi-command",
			"Path or command to be used for the nvidia-smi executable").
			Default(exporter.DefaultNvidiaSmiCommand).String()
	)

	kingpin.Parse()
	qFields, err := exporter.ParseAutoQFields(*nvidiaSmiCommand)
	if err != nil {
		fmt.Printf("error: %v\n", err)
		os.Exit(1)
	}
	fields := exporter.QFieldSliceToStringSlice(qFields)
	fmt.Printf("Fields:\n\n%s\n", strings.Join(fields, "\n"))
}
