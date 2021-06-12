package main

import (
	"flag"
	"fmt"
	"nvidia-smi-exporter/internal/exporter"
	"os"
	"strings"
)

func main() {
	var nvidiaSmiCommand string
	flag.StringVar(&nvidiaSmiCommand, "nvidia-smi-command", "nvidia-smi", "command to run nvidia-smi")
	flag.Parse()

	fields, err := exporter.ParseQueryFields(nvidiaSmiCommand)
	if err != nil {
		fmt.Printf("error: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Fields: %s\n", strings.Join(fields, ","))
}
