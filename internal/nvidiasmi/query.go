package nvidiasmi

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// DefaultCommand is the default command used to reach nvidia-smi.
const DefaultCommand = "nvidia-smi"

// RunFunc runs a prepared nvidia-smi command. It is the injectable seam that
// lets tests substitute canned output for a real process.
type RunFunc func(cmd *exec.Cmd) error

// DefaultRunFunc runs the command as prepared.
func DefaultRunFunc(cmd *exec.Cmd) error {
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("error running command: %w", err)
	}

	return nil
}

// Query runs nvidia-smi --query-gpu for the given fields and parses the CSV
// output into a table, returning the command's exit code alongside.
func Query(ctx context.Context, command string, qFields []QField, run RunFunc) (*Table, int, error) {
	qFieldsJoined := strings.Join(QFieldSliceToStringSlice(qFields), ",")

	cmdAndArgs := strings.Fields(command)
	cmdAndArgs = append(cmdAndArgs, "--query-gpu="+qFieldsJoined)
	cmdAndArgs = append(cmdAndArgs, "--format=csv")

	var stdout bytes.Buffer

	var stderr bytes.Buffer

	cmd := exec.CommandContext(ctx, cmdAndArgs[0], cmdAndArgs[1:]...) //nolint:gosec
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := run(cmd)
	if err != nil {
		exitCode := -1

		var exitError *exec.ExitError
		if errors.As(err, &exitError) {
			exitCode = exitError.ExitCode()
		}

		return nil, exitCode, fmt.Errorf(
			"command failed: code: %d | command: %s | stdout: %s | stderr: %s: %w",
			exitCode,
			strings.Join(cmdAndArgs, " "),
			stdout.String(),
			stderr.String(),
			err,
		)
	}

	table, err := ParseCSVIntoTable(strings.TrimSpace(stdout.String()), qFields)
	if err != nil {
		return nil, -1, err
	}

	return &table, 0, nil
}
