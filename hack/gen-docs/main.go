// Command gen-docs updates the generated sections of docs/CONFIGURE.md (--help
// output and embedded demo capture list).
//
// Pass -check to verify that the committed file already matches without writing.
package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"runtime"
	"slices"
	"strings"

	"github.com/utkuozdemir/nvidia_gpu_exporter/internal/app"
	"github.com/utkuozdemir/nvidia_gpu_exporter/internal/demodata"
)

const configureDocPath = "docs/CONFIGURE.md"

func main() {
	check := flag.Bool("check", false, "verify generated files are up to date without writing")

	flag.Parse()

	if err := run(*check); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(check bool) error {
	// exporter-toolkit registers --[no-]web.systemd-socket only on Linux,
	// which also shifts the flag column alignment in --help.
	if runtime.GOOS != "linux" {
		return errors.New("gen-docs must run on linux (use: task docs or task fmt)")
	}

	root, err := os.OpenRoot(".")
	if err != nil {
		return fmt.Errorf("open workspace root: %w", err)
	}

	defer func() { _ = root.Close() }()

	return syncConfigureDoc(root, check)
}

func syncConfigureDoc(root *os.Root, check bool) error {
	raw, err := root.ReadFile(configureDocPath)
	if err != nil {
		return fmt.Errorf("read %s: %w", configureDocPath, err)
	}

	helpText, err := renderHelp()
	if err != nil {
		return err
	}

	capturesBlock, err := renderDemoCaptures()
	if err != nil {
		return err
	}

	updated, err := replaceBetween(
		string(raw),
		"This is the output of `nvidia_gpu_exporter --help`:\n\n```text\n",
		"\n```\n",
		helpText,
	)
	if err != nil {
		return fmt.Errorf("%s (--help block): %w", configureDocPath, err)
	}

	updated, err = replaceBetween(
		updated,
		"```yaml\n# Embedded captures:\n",
		"\nstate:",
		capturesBlock,
	)
	if err != nil {
		return fmt.Errorf("%s (demo captures block): %w", configureDocPath, err)
	}

	if updated == string(raw) {
		return nil
	}

	if check {
		return fmt.Errorf("%s is out of date, run: task docs", configureDocPath)
	}

	if err := root.WriteFile(configureDocPath, []byte(updated), 0o600); err != nil {
		return fmt.Errorf("write %s: %w", configureDocPath, err)
	}

	return nil
}

func renderHelp() (string, error) {
	var buf bytes.Buffer

	err := app.Run(context.Background(), []string{"--help"}, app.Options{
		UsageWriter: &buf,
		Terminate:   func(int) {},
	})
	if err != nil {
		return "", fmt.Errorf("render --help: %w", err)
	}

	help := strings.TrimRight(buf.String(), "\n")
	if strings.TrimSpace(help) == "" {
		return "", errors.New("--help produced empty output")
	}

	return help, nil
}

func renderDemoCaptures() (string, error) {
	names, err := fs.Glob(demodata.FS, "*.txt")
	if err != nil {
		return "", fmt.Errorf("glob demodata: %w", err)
	}

	if len(names) == 0 {
		return "", errors.New("no embedded demo captures found")
	}

	// List the default capture first so the comment order matches the
	// default `capture:` value below it.
	slices.SortFunc(names, func(left, right string) int {
		if left == demodata.Default && right != demodata.Default {
			return -1
		}

		if right == demodata.Default && left != demodata.Default {
			return 1
		}

		return strings.Compare(left, right)
	})

	var builder strings.Builder
	for _, name := range names {
		builder.WriteString("# ")
		builder.WriteString(name)
		builder.WriteByte('\n')
	}

	builder.WriteString("capture: ")
	builder.WriteString(demodata.Default)

	return builder.String(), nil
}

func replaceBetween(doc, startMarker, endMarker, replacement string) (string, error) {
	before, rest, ok := strings.Cut(doc, startMarker)
	if !ok {
		return "", fmt.Errorf("start marker %q not found", startMarker)
	}

	_, after, ok := strings.Cut(rest, endMarker)
	if !ok {
		return "", fmt.Errorf("end marker %q not found after %q", endMarker, startMarker)
	}

	return before + startMarker + replacement + endMarker + after, nil
}
