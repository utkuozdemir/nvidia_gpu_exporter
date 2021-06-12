package exporter

import (
	"bytes"
	"os/exec"
	"regexp"
	"strings"
)

var (
	fieldRegex = regexp.MustCompile(`(?m)\n\s*\n^"([^"]+)"`)
)

func ParseQueryFields(nvidiaSmiCommand string) ([]string, error) {
	cmdAndArgs := strings.Fields(nvidiaSmiCommand)
	cmdAndArgs = append(cmdAndArgs, "--help-query-gpu")
	cmd := exec.Command(cmdAndArgs[0], cmdAndArgs[1:]...)

	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	err := cmd.Run()
	if err != nil {
		return nil, err
	}

	out := stdout.String()
	found := fieldRegex.FindAllStringSubmatch(out, -1)

	var fields []string
	for _, ss := range found {
		fields = append(fields, ss[1])
	}

	return fields, nil
}
