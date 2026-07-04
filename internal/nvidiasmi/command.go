package nvidiasmi

import (
	"errors"
	"fmt"
	"strings"

	"github.com/kballard/go-shellquote"
)

// errEmptyCommand is returned when the configured command string contains no
// executable at all.
var errEmptyCommand = errors.New("empty nvidia-smi command")

// SplitCommand splits the configured nvidia-smi command string into the
// argument vector to exec. A string without quote characters splits on
// whitespace, exactly as it always has. A string containing a quote character
// is split with POSIX shell quoting rules instead, so an executable path with
// spaces in it (common on Windows) can be quoted. Quoting is opt-in via that
// check on purpose: a POSIX split would treat the backslashes of an unquoted
// Windows path as escapes and mangle setups that work today.
func SplitCommand(command string) ([]string, error) {
	parts := strings.Fields(command)

	if strings.ContainsAny(command, `"'`) {
		var err error

		parts, err = shellquote.Split(command)
		if err != nil {
			return nil, fmt.Errorf("failed to split command %q: %w", command, err)
		}
	}

	if len(parts) == 0 || parts[0] == "" {
		return nil, errEmptyCommand
	}

	return parts, nil
}
