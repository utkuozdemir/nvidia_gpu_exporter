package nvmlnative

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/utkuozdemir/nvidia_gpu_exporter/internal/captures"
)

// TestSymbolInventoriesCoverRequirements checks the requirement manifest
// against every capture carrying an nvml-symbols section. The newest such
// capture fails hard on a missing symbol (the driver dropped or renamed an
// entry point this backend calls); older ones report, since old drivers
// legitimately predate newer APIs.
func TestSymbolInventoriesCoverRequirements(t *testing.T) {
	t.Parallel()

	names := linuxCaptureNames(t)

	var withInventory []string

	inventories := map[string]map[string]bool{}

	for _, name := range names {
		symbols := captureSymbolInventory(t, name)
		if symbols != nil {
			withInventory = append(withInventory, name)
			inventories[name] = symbols
		}
	}

	require.NotEmpty(t, withInventory,
		"no capture carries an nvml-symbols section; the symbol drift check is inert")

	newest := newestByDriverVersion(t, withInventory)
	t.Logf("captures with a symbol inventory: %v (hard assertions: %s)", withInventory, newest)

	for _, name := range withInventory {
		symbols := inventories[name]

		for _, requirement := range nvmlSymbolRequirements {
			if anySymbolPresent(symbols, requirement.anyOf) {
				continue
			}

			message := "driver library lacks a required NVML entry point\n" +
				"  capture: %s\n  call site: %s (serves: %s)\n  accepted symbols: %v\n" +
				"  fix: if the driver renamed it, add the new spelling to nvmlSymbolRequirements in " +
				"internal/nvmlnative/symbols.go and wire it in backend_nvml.go; " +
				"if it was removed, the affected fields need a new source or a recorded deferral"

			if name == newest {
				t.Errorf(message, name, requirement.goCall, requirement.serves, requirement.anyOf)
			} else {
				t.Logf("(older driver) "+message, name, requirement.goCall, requirement.serves, requirement.anyOf)
			}
		}
	}
}

func anySymbolPresent(inventory map[string]bool, alternatives []string) bool {
	for _, symbol := range alternatives {
		if inventory[symbol] {
			return true
		}
	}

	return false
}

// captureSymbolInventory extracts the nvml-symbols section, nil when the
// capture has none.
func captureSymbolInventory(t *testing.T, name string) map[string]bool {
	t.Helper()

	data, err := captures.FS.ReadFile(name)
	require.NoError(t, err)

	marker := "# capabilities :: nvml-symbols"

	idx := strings.Index(string(data), marker)
	if idx < 0 {
		return nil
	}

	symbols := map[string]bool{}

	for line := range strings.SplitSeq(string(data)[idx:], "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "nvml") {
			symbols[line] = true
		}

		// a blank line after symbols started means the section ended
		if line == "" && len(symbols) > 0 {
			break
		}
	}

	require.NotEmpty(t, symbols, "capture %s has an nvml-symbols marker but no symbols", name)

	return symbols
}

// TestSymbolManifestCoversCollectorCallSites parses the collector source and
// fails when an NVML call site has no entry in the requirement manifest, so
// the manifest cannot silently rot as collectors are added.
func TestSymbolManifestCoversCollectorCallSites(t *testing.T) {
	t.Parallel()

	fset := token.NewFileSet()

	file, err := parser.ParseFile(fset, "backend_nvml.go", nil, 0)
	require.NoError(t, err)

	manifest := map[string]bool{}
	for _, requirement := range nvmlSymbolRequirements {
		manifest[requirement.goCall] = true
	}

	// call sites appear in two shapes: method calls on a device value
	// (dev.GetName, mig handlers) and the nvmlAPI seam construction in
	// realNVML, whose field names are the manifest's core goCall keys.
	seen := map[string]bool{}

	ast.Inspect(file, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}

		selector, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}

		method := selector.Sel.Name
		if strings.HasPrefix(method, "Get") || method == "ValidateInforom" {
			// skip go-nvml package-level helpers reached through the seam
			if ident, isIdent := selector.X.(*ast.Ident); isIdent && ident.Name == "nvml" {
				return true
			}

			// versioned handler sub-calls are covered by their parent getter
			if method == "GetGpuFabricInfo" {
				return true
			}

			seen[method] = true
		}

		return true
	})

	require.NotEmpty(t, seen)

	for method := range seen {
		assert.True(t, manifest[method],
			"collector calls %s but nvmlSymbolRequirements has no entry for it\n"+
				"  fix: add a symbolRequirement in internal/nvmlnative/symbols.go", method)
	}
}
