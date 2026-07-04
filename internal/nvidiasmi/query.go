package nvidiasmi

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// DefaultCommand is the default command used to reach nvidia-smi.
const DefaultCommand = "nvidia-smi"

// waitDelay bounds how long a killed process can keep the run blocked after
// its context fires, covering processes that ignore the kill signal or keep
// their output pipes open. Without it a wedged nvidia-smi would hold the
// collection forever despite the timeout.
const waitDelay = 2 * time.Second

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

	stdout, exitCode, err := execQuery(ctx, command, run, "--query-gpu="+qFieldsJoined, "--format=csv")
	if err != nil {
		return nil, exitCode, err
	}

	table, err := ParseCSVIntoTable(strings.TrimSpace(stdout), qFields)
	if err != nil {
		return nil, -1, err
	}

	return &table, 0, nil
}

// execQuery runs the nvidia-smi command with the given arguments appended,
// returning its stdout and exit code.
func execQuery(ctx context.Context, command string, run RunFunc, args ...string) (string, int, error) {
	cmdAndArgs, err := SplitCommand(command)
	if err != nil {
		return "", -1, err
	}

	cmdAndArgs = append(cmdAndArgs, args...)

	var stdout bytes.Buffer

	var stderr bytes.Buffer

	cmd := exec.CommandContext(ctx, cmdAndArgs[0], cmdAndArgs[1:]...) //nolint:gosec
	cmd.WaitDelay = waitDelay
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err = run(cmd)
	if err != nil {
		exitCode := -1

		var exitError *exec.ExitError
		if errors.As(err, &exitError) {
			exitCode = exitError.ExitCode()
		}

		return "", exitCode, fmt.Errorf(
			"command failed: code: %d | command: %s | stdout: %s | stderr: %s: %w",
			exitCode,
			strings.Join(cmdAndArgs, " "),
			stdout.String(),
			stderr.String(),
			err,
		)
	}

	return stdout.String(), 0, nil
}
