package main

import (
	"fmt"
	"gopkg.in/alecthomas/kingpin.v2"
	"nvidia_gpu_exporter/internal/exporter"
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
	fields, err := exporter.ParseQueryFields(*nvidiaSmiCommand)
	if err != nil {
		fmt.Printf("error: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Fields:\n\n%s\n", strings.Join(fields, "\n"))
}
