package main

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
)

var (
	fieldRegex = regexp.MustCompile(`(?m)\n\s*\n^"([^"]+)"`)
)

func main() {
	var nvidiaSmiCommand string
	flag.StringVar(&nvidiaSmiCommand, "nvidia-smi-command", "nvidia-smi", "command to run nvidia-smi")
	flag.Parse()

	cmdAndArgs := strings.Fields(nvidiaSmiCommand)
	cmdAndArgs = append(cmdAndArgs, "--help-query-gpu")
	cmd := exec.Command(cmdAndArgs[0], cmdAndArgs[1:]...)

	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	err := cmd.Run()
	if err != nil {
		fmt.Printf("error: %v\n", err)
		os.Exit(1)
	}

	out := stdout.String()
	found := fieldRegex.FindAllStringSubmatch(out, -1)

	var fields []string
	for _, ss := range found {
		fields = append(fields, ss[1])
	}

	fmt.Printf("Fields: %s\n", strings.Join(fields, ","))
}
