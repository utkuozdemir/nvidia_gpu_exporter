//go:build linux && cgo

package nvmlnative

import (
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
	"github.com/NVIDIA/go-nvml/pkg/nvml/mock"
	"github.com/neilotoole/slogt/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCollectionMakesNoCallWhenNoSymbolIsAvailable is the fail-closed proof for
// the whole guard. A driver library that exports none of the entry points must
// produce a scrape that reaches zero NVML getters.
//
// The device mock is deliberately bare: go-nvml's generated mock panics on any
// method whose stub is nil, so *reaching* any getter fails the test loudly.
// Completing the collection is therefore evidence that the guard blocked every
// call, and unlike a static analysis it understands loops, batched calls and
// getters shared between the field, MIG, per-process and XID paths.
func TestCollectionMakesNoCallWhenNoSymbolIsAvailable(t *testing.T) {
	t.Parallel()

	// the only stub: uuid, which every per-GPU series is labeled by. Every
	// other getter is nil, so reaching one panics the mock and fails the test.
	bare := &mock.Device{ //nolint:exhaustruct // bare on purpose: any other call panics
		GetUUIDFunc: func() (string, nvml.Return) { return "GPU-1", nvml.SUCCESS },
	}

	fake := &fakeAPI{devices: []nvml.Device{bare}}

	api := fake.api()
	// uuid is a startup requirement, so it stays available; everything else is
	// absent, which is the case the guard has to survive
	api.lookupSymbol = func(name string) error {
		if name == "nvmlDeviceGetUUID" {
			return nil
		}

		return errors.New("symbol not found")
	}

	backend, err := newWithAPI(api, slogt.New(t))
	require.NoError(t, err)

	fields := resolveFields(t, "AUTO")

	opts := CollectOptions{ComputeApps: true, PCIeThroughput: true, Energy: true, MIG: true}

	// must not panic: every getter is unavailable, so none may be reached
	reading, _, err := backend.QueryFunc(fields, opts)(t.Context())
	require.NoError(t, err)
	require.NotNil(t, reading.Table)
}

// TestGuardedSymbolsMatchTheGuard keeps the probed symbol list in lockstep with
// the adapter. Every symbol the adapter checks must be probed at startup, and
// every probed symbol must be checked by something, so neither list can rot
// into a guard that is never armed or a probe that protects nothing.
func TestGuardedSymbolsMatchTheGuard(t *testing.T) {
	t.Parallel()

	var source []byte

	for _, name := range []string{
		"guard_nvml.go", "backend_nvml.go", "mig_nvml.go", "xid_nvml.go",
	} {
		part, err := os.ReadFile(name)
		require.NoError(t, err)

		source = append(source, part...)
	}

	// every NVML symbol name the adapter mentions, however the call is wrapped
	referenced := map[string]bool{}
	for _, match := range regexp.MustCompile(`"(nvml[A-Z][A-Za-z0-9_]+)"`).FindAllStringSubmatch(string(source), -1) {
		referenced[match[1]] = true
	}

	probed := map[string]bool{}
	for _, symbol := range guardedSymbols {
		probed[symbol] = true
	}

	for symbol := range referenced {
		assert.True(t, probed[symbol],
			"the adapter guards %s but guardedSymbols does not probe it,\n"+
				"  so the guard is never armed: add it to guardsymbols_nvml.go", symbol)
	}

	for symbol := range probed {
		// the seam-level guards live outside the adapter, so allow a probed
		// symbol that no adapter method references
		if referenced[symbol] || strings.HasSuffix(symbol, "ValidateInforom") {
			continue
		}

		assert.Fail(t, "unused probe",
			"guardedSymbols probes %s but nothing guards on it", symbol)
	}
}

// TestCollectorDoesNotCallRawDeviceMethods enforces the boundary itself: only
// the adapter may hold an nvml.Device and call methods on it. Collector code
// must go through the guarded interface, so that a newly added getter cannot
// reach the driver without a probe. This is the invariant that keeps the class
// of bug closed rather than merely fixing the instances known today.
func TestCollectorDoesNotCallRawDeviceMethods(t *testing.T) {
	t.Parallel()

	entries, err := os.ReadDir(".")
	require.NoError(t, err)

	fset := token.NewFileSet()

	for _, entry := range entries {
		name := entry.Name()
		// the adapter is where raw handles legitimately live
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") || name == "guard_nvml.go" {
			continue
		}

		file, parseErr := parser.ParseFile(fset, name, nil, 0)
		require.NoError(t, parseErr)

		assertNoRawDeviceType(t, fset, file, name)
	}
}

// assertNoRawDeviceType reports declarations outside the adapter that name
// nvml.Device as a parameter, which would hand collector code an unguarded
// handle. The seam struct and the raw-keyed XID map are the deliberate
// exceptions, both documented at their definitions.
func assertNoRawDeviceType(t *testing.T, fset *token.FileSet, file *ast.File, name string) {
	t.Helper()

	allowed := map[string]bool{
		"nvmlAPI":     true, // the seam holds raw handles by design
		"realNVML":    true,
		"xidRegister": true, // events come back keyed by the raw handle
		"recordXID":   true,
	}

	ast.Inspect(file, func(n ast.Node) bool {
		decl, ok := n.(*ast.FuncDecl)
		if !ok || allowed[decl.Name.Name] {
			return true
		}

		for _, param := range decl.Type.Params.List {
			sel, isSel := param.Type.(*ast.SelectorExpr)
			if !isSel || sel.Sel.Name != "Device" {
				continue
			}

			assert.Fail(t, "raw device outside the adapter",
				"%s: %s takes an nvml.Device; collector code must take the guarded "+
					"device interface so every entry point is probed first",
				fset.Position(decl.Pos()), decl.Name.Name)
		}

		return true
	})

	_ = name
}

// TestNewRefusesWithoutTheIdentityExport pins the one export that cannot
// degrade. Without it every GPU reports the same uuid label and their series
// collapse into one, so starting would publish a corrupt scrape.
func TestNewRefusesWithoutTheIdentityExport(t *testing.T) {
	t.Parallel()

	fake := &fakeAPI{devices: []nvml.Device{identityDevice()}}

	api := fake.api()
	api.lookupSymbol = func(name string) error {
		if name == "nvmlDeviceGetUUID" {
			return errors.New("symbol not found")
		}

		return nil
	}

	_, err := newWithAPI(api, slogt.New(t))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "nvmlDeviceGetUUID")
}
