//go:build windows

package main

import (
	"fmt"
	"log/slog"
	"os"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc/eventlog"
	"golang.org/x/sys/windows/svc/mgr"
)

// installService registers the exporter with the service control manager. Any
// args are baked into the service command line, so users configure the service
// by passing the same flags they would use interactively, for example:
//
//	nvidia_gpu_exporter install --web.listen-address=:9836
//
// Installing is idempotent: re-running it reconfigures an already installed
// service in place (for example to change the baked-in flags) instead of failing.
func installService(args []string) error {
	exePath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("failed to determine executable path: %w", err)
	}

	manager, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("failed to connect to service manager: %w", err)
	}
	defer manager.Disconnect() //nolint:errcheck

	config := mgr.Config{
		ServiceType:    windows.SERVICE_WIN32_OWN_PROCESS,
		StartType:      mgr.StartAutomatic,
		DisplayName:    "Nvidia GPU Exporter",
		Description:    "Exports Nvidia GPU metrics in Prometheus format.",
		BinaryPathName: composeBinaryPath(exePath, args),
	}

	service, updated, err := installOrUpdateService(manager, exePath, args, config)
	if err != nil {
		return err
	}
	defer service.Close()

	// Restart the service if it fails, resetting the failure count after a day.
	const failureResetSeconds = 86400

	if recErr := service.SetRecoveryActions(
		[]mgr.RecoveryAction{{Type: mgr.ServiceRestart, Delay: 5 * time.Second}},
		failureResetSeconds,
	); recErr != nil {
		slog.Warn("failed to set service recovery actions", "err", recErr)
	}

	// By default Windows only runs recovery actions when the process crashes
	// without reporting a stopped state. The exporter reports a clean stop with a
	// non-zero exit code on errors (a bind failure, or shutdown-on-error), so the
	// recovery actions must also cover those non-crash failures to be useful.
	if recErr := service.SetRecoveryActionsOnNonCrashFailures(true); recErr != nil {
		slog.Warn("failed to enable recovery on non-crash failures", "err", recErr)
	}

	// The event log source values are static, so register it only on a fresh
	// install. Reconfiguring an existing service leaves the source in place.
	if !updated {
		if logErr := eventlog.InstallAsEventCreate(
			serviceName,
			eventlog.Info|eventlog.Warning|eventlog.Error,
		); logErr != nil {
			slog.Warn("failed to register event log source", "err", logErr)
		}
	}

	action := "service installed"
	if updated {
		action = "service reconfigured"
	}

	slog.Info(action, "name", serviceName, "path", exePath, "args", args)

	return nil
}

// installOrUpdateService creates the service, or reconfigures it in place if it
// already exists, making install idempotent. It reports whether an existing
// service was updated.
func installOrUpdateService(
	manager *mgr.Mgr,
	exePath string,
	args []string,
	config mgr.Config,
) (*mgr.Service, bool, error) {
	if existing, openErr := manager.OpenService(serviceName); openErr == nil {
		if updateErr := existing.UpdateConfig(config); updateErr != nil {
			existing.Close()

			return nil, false, fmt.Errorf("failed to update existing service: %w", updateErr)
		}

		return existing, true, nil
	}

	service, err := manager.CreateService(serviceName, exePath, config, args...)
	if err != nil {
		return nil, false, fmt.Errorf("failed to create service: %w", err)
	}

	return service, false, nil
}

// composeBinaryPath builds the service command line the same way CreateService
// does, so an in-place UpdateConfig produces an identical binary path.
func composeBinaryPath(exePath string, args []string) string {
	var builder strings.Builder

	builder.WriteString(syscall.EscapeArg(exePath))

	for _, arg := range args {
		builder.WriteString(" ")
		builder.WriteString(syscall.EscapeArg(arg))
	}

	return builder.String()
}

// uninstallService removes the exporter service and its event log source.
func uninstallService() error {
	manager, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("failed to connect to service manager: %w", err)
	}
	defer manager.Disconnect() //nolint:errcheck

	service, err := manager.OpenService(serviceName)
	if err != nil {
		return fmt.Errorf("service %q is not installed: %w", serviceName, err)
	}
	defer service.Close()

	if delErr := service.Delete(); delErr != nil {
		return fmt.Errorf("failed to delete service: %w", delErr)
	}

	if logErr := eventlog.Remove(serviceName); logErr != nil {
		slog.Warn("failed to remove event log source", "err", logErr)
	}

	slog.Info("service uninstalled", "name", serviceName)

	return nil
}
