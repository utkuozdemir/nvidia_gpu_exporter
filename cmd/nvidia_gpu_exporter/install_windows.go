//go:build windows

package main

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc"
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

// uninstallService stops the exporter service if it is running, then removes
// it and its event log source. Deleting a running service would only mark it
// for deletion: it keeps running, the name stays claimed until it stops, and
// a reinstall in that window fails with "marked for deletion".
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

	if stopErr := stopService(service); stopErr != nil {
		service.Close()

		return fmt.Errorf("failed to stop service before removal: %w", stopErr)
	}

	// a service left marked for deletion by an earlier binary (which deleted
	// without stopping) answers a repeated delete with "already marked";
	// the stop above was the missing piece, so that is success, not failure
	delErr := service.Delete()
	// deletion only completes once the last open handle closes, and ours is
	// one of them: close it before waiting for the entry to disappear
	service.Close()

	if delErr != nil && !errors.Is(delErr, windows.ERROR_SERVICE_MARKED_FOR_DELETE) {
		return fmt.Errorf("failed to delete service: %w", delErr)
	}

	waitServiceGone(manager)

	if logErr := eventlog.Remove(serviceName); logErr != nil {
		slog.Warn("failed to remove event log source", "err", logErr)
	}

	slog.Info("service uninstalled", "name", serviceName)

	return nil
}

// stopTimeout bounds how long an uninstall waits for the service to stop
// before giving up.
const stopTimeout = 30 * time.Second

// stopPollInterval is the polling cadence while waiting on service state.
const stopPollInterval = 300 * time.Millisecond

// stopService drives a service to the stopped state, bounded by a timeout. A
// stopped service is left alone; a starting or stopping one is waited for (a
// service in a pending state rejects controls); anything else is sent the
// stop control, tolerating the benign races Windows documents: the service
// may stop on its own or enter a pending state between the query and the
// control, which answers with not-active or cannot-accept-control and is
// resolved by the next query, not a failure.
func stopService(service *mgr.Service) error {
	deadline := time.Now().Add(stopTimeout)
	requested := false

	for {
		status, err := service.Query()
		if err != nil {
			return fmt.Errorf("failed to query service state: %w", err)
		}

		switch {
		case status.State == svc.Stopped:
			slog.Info("service is stopped", "name", serviceName)

			return nil
		case status.State == svc.StartPending || status.State == svc.StopPending:
			// wait for a controllable or final state
		case !requested:
			// also covers the paused states: a paused service accepts stop
			slog.Info("stopping the service", "name", serviceName, "state", status.State)

			sent, sendErr := sendStop(service)
			if sendErr != nil {
				return sendErr
			}

			requested = sent
		}

		if time.Now().After(deadline) {
			return fmt.Errorf("service did not stop within %s, last observed state: %d", stopTimeout, status.State)
		}

		time.Sleep(stopPollInterval)
	}
}

// sendStop issues the stop control, tolerating the benign races Windows
// documents: the service may stop on its own or enter a pending state
// between the caller's query and this control, answering with not-active or
// cannot-accept-control. Those resolve through the caller's next query, so
// they report an unsent control rather than an error.
func sendStop(service *mgr.Service) (bool, error) {
	_, err := service.Control(svc.Stop)
	if err == nil {
		return true, nil
	}

	if errors.Is(err, windows.ERROR_SERVICE_NOT_ACTIVE) ||
		errors.Is(err, windows.ERROR_SERVICE_CANNOT_ACCEPT_CTRL) {
		return false, nil
	}

	return false, fmt.Errorf("failed to send the stop control: %w", err)
}

// goneTimeout bounds the best-effort wait for the deleted service to leave
// the service database.
const goneTimeout = 5 * time.Second

// waitServiceGone waits, best-effort, for the deleted service's entry to
// disappear: removal completes only when the last open handle closes, and a
// lingering entry would fail an immediate reinstall. A handle held by
// another process (an open Services console, a monitoring agent) is warned
// about rather than failed on, since removal completes by itself the moment
// that handle closes.
func waitServiceGone(manager *mgr.Mgr) {
	deadline := time.Now().Add(goneTimeout)

	for {
		service, err := manager.OpenService(serviceName)
		if err != nil {
			return
		}

		service.Close()

		if time.Now().After(deadline) {
			slog.Warn("service is still registered: another process holds a handle to it, "+
				"removal completes when that handle closes", "name", serviceName)

			return
		}

		time.Sleep(stopPollInterval)
	}
}
