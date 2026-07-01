package nvidiasmi_test

import (
	"errors"
	"os/exec"
	"slices"
	"testing"
	"time"

	"github.com/neilotoole/slogt/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/utkuozdemir/nvidia_gpu_exporter/internal/nvidiasmi"
)

var errNoQuery = errors.New("query refused")

// TestResolveFieldsAutoFallsBackOnMappingFailure covers the case where field
// discovery succeeds and reports a field the built-in mapping does not know
// (a newer driver), but the initial mapping query fails. Auto mode must come
// up with the built-in list instead of failing startup.
func TestResolveFieldsAutoFallsBackOnMappingFailure(t *testing.T) {
	t.Parallel()

	helpText := "List of valid properties:\n\n\"uuid\"\n\n\"name\"\n\n\"brand_new.field\"\n"

	run := func(cmd *exec.Cmd) error {
		if slices.Contains(cmd.Args, "--help-query-gpu") {
			_, _ = cmd.Stdout.Write([]byte(helpText))

			return nil
		}

		return errNoQuery
	}

	resolved, err := nvidiasmi.ResolveFields(
		t.Context(),
		"nvidia-smi",
		nvidiasmi.DefaultQField,
		"",
		time.Second,
		run,
		slogt.New(t),
	)

	require.NoError(t, err)
	assert.NotEmpty(t, resolved.Query)
	assert.NotEmpty(t, resolved.Returned)
	assert.NotEmpty(t, resolved.Info)

	// the unknown discovered field is gone along with the discovered list
	assert.NotContains(t, resolved.Returned, nvidiasmi.QField("brand_new.field"))
	assert.Contains(t, resolved.Returned, nvidiasmi.QField("uuid"))
}

// TestResolveFieldsAutoUsesDiscoveredListOnMappingFallback covers the milder
// failure: the mapping query fails but every discovered field is known to the
// built-in mapping, so the discovered list survives with built-in names.
func TestResolveFieldsAutoUsesDiscoveredListOnMappingFallback(t *testing.T) {
	t.Parallel()

	helpText := "List of valid properties:\n\n\"uuid\"\n\n\"name\"\n\n\"fan.speed\"\n"

	run := func(cmd *exec.Cmd) error {
		if slices.Contains(cmd.Args, "--help-query-gpu") {
			_, _ = cmd.Stdout.Write([]byte(helpText))

			return nil
		}

		return errNoQuery
	}

	resolved, err := nvidiasmi.ResolveFields(
		t.Context(),
		"nvidia-smi",
		nvidiasmi.DefaultQField,
		"",
		time.Second,
		run,
		slogt.New(t),
	)

	require.NoError(t, err)
	assert.Equal(t, nvidiasmi.RField("fan.speed [%]"), resolved.Returned["fan.speed"])
}

// TestResolveFieldsExplicitUnknownFieldStillFails pins the explicit-list
// contract: a user-provided field the built-in mapping cannot cover fails
// startup when the mapping query fails, instead of being silently replaced.
func TestResolveFieldsExplicitUnknownFieldStillFails(t *testing.T) {
	t.Parallel()

	run := func(_ *exec.Cmd) error {
		return errNoQuery
	}

	_, err := nvidiasmi.ResolveFields(
		t.Context(),
		"nvidia-smi",
		"uuid,name,brand_new.field",
		"",
		time.Second,
		run,
		slogt.New(t),
	)

	require.ErrorContains(t, err, "brand_new.field")
}
