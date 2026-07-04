package main

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"os/exec"
	"os/signal"
	"syscall"

	"github.com/utkuozdemir/nvidia_gpu_exporter/internal/app"
)

// main is the entrypoint of the application.
func main() {
	if err := dispatch(); err != nil {
		slog.Default().Error("failed to run", "err", err)

		var exitErr *exec.ExitError

		if errors.As(err, &exitErr) {
			os.Exit(exitErr.ExitCode())
		}

		os.Exit(1)
	}
}

// runInteractive runs the exporter in the foreground, cancelling on SIGINT or
// SIGTERM. This is the only mode on non-Windows platforms, and the mode used on
// Windows when not started by the service control manager.
func runInteractive() error {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	return run(ctx, nil)
}

// run starts the exporter through the shared entry, with the process
// arguments and the process-wide default logger the binary is expected to
// install.
func run(ctx context.Context, extraLogHandler slog.Handler) error {
	//nolint:wrapcheck // the shared entry wraps its own errors.
	return app.Run(ctx, os.Args[1:], app.Options{
		ExtraLogHandler:  extraLogHandler,
		SetDefaultLogger: true,
	})
}
