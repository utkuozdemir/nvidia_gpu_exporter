//go:build windows

package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/debug"
	"golang.org/x/sys/windows/svc/eventlog"
)

// serviceName is the name the exporter registers under with the Windows service
// control manager.
const serviceName = "nvidia_gpu_exporter"

// dispatch decides how to run on Windows: install/uninstall the service, run
// under the service control manager when started by it, or run interactively.
func dispatch() error {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "install":
			return installService(os.Args[2:])
		case "uninstall":
			return uninstallService()
		}
	}

	isService, err := svc.IsWindowsService()
	if err != nil {
		return fmt.Errorf("failed to determine if running as a windows service: %w", err)
	}

	if !isService {
		return runInteractive()
	}

	return runService()
}

// runService runs the exporter under the service control manager. A service has
// no console, so logs are mirrored into the Windows event log when available.
func runService() error {
	var logHandler slog.Handler

	// Mirroring logs into the event log is best-effort: a service whose event log
	// source was never registered (for example one created directly with
	// `sc create`) must still start rather than fail with a 1053 timeout.
	if elog, err := eventlog.Open(serviceName); err != nil {
		slog.Warn("failed to open event log, continuing without event log mirroring", "err", err)
	} else {
		defer elog.Close()

		logHandler = newEventLogHandler(elog)
	}

	handler := &windowsService{logHandler: logHandler}

	if err := svc.Run(serviceName, handler); err != nil {
		return fmt.Errorf("windows service failed: %w", err)
	}

	return nil
}

// windowsService implements svc.Handler, bridging service control requests to
// the exporter's context-based shutdown.
type windowsService struct {
	logHandler slog.Handler
}

// Execute is invoked by the service control manager. It starts the exporter and
// translates Stop and Shutdown requests into context cancellation, reporting
// state transitions back to the manager.
func (ws *windowsService) Execute(
	_ []string,
	requests <-chan svc.ChangeRequest,
	changes chan<- svc.Status,
) (bool, uint32) {
	const accepted = svc.AcceptStop | svc.AcceptShutdown

	changes <- svc.Status{State: svc.StartPending}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	runErr := make(chan error, 1)

	go func() {
		runErr <- run(ctx, ws.logHandler)
	}()

	changes <- svc.Status{State: svc.Running, Accepts: accepted}

loop:
	for {
		select {
		case request, ok := <-requests:
			if !ok {
				// The service manager closed the request channel; stop cleanly.
				break loop
			}

			//nolint:exhaustive // only the control requests we accept are relevant.
			switch request.Cmd {
			case svc.Interrogate:
				changes <- request.CurrentStatus
			case svc.Stop, svc.Shutdown:
				break loop
			default:
			}
		case err := <-runErr:
			// The exporter exited on its own, before any stop request. Let the
			// return value carry the final stopped state and exit code: svc.Run
			// reports SERVICE_STOPPED once Execute returns, and reporting it here
			// too would send a clean stop before the real exit code lands.
			if err != nil {
				return true, 1
			}

			return false, 0
		}
	}

	// Stop or shutdown request (or a closed channel): cancel the run and wait for
	// it. svc.Run reports the single SERVICE_STOPPED from the return value, so we
	// only signal the pending transition here.
	changes <- svc.Status{State: svc.StopPending}

	cancel()
	<-runErr

	return false, 0
}

// eventLogHandler is a slog.Handler that writes records to the Windows event log.
type eventLogHandler struct {
	log   debug.Log
	attrs []slog.Attr
}

func newEventLogHandler(log debug.Log) *eventLogHandler {
	return &eventLogHandler{log: log}
}

// Enabled always returns true. The tee that wraps this handler applies the
// configured log level, so the event log mirrors exactly what the logger emits.
func (h *eventLogHandler) Enabled(_ context.Context, _ slog.Level) bool {
	return true
}

func (h *eventLogHandler) Handle(_ context.Context, record slog.Record) error {
	var builder strings.Builder

	builder.WriteString(record.Message)

	for _, attr := range h.attrs {
		writeAttr(&builder, attr)
	}

	record.Attrs(func(attr slog.Attr) bool {
		writeAttr(&builder, attr)

		return true
	})

	// Without a registered message file the event viewer cannot map IDs to
	// templates, so a single ID is enough; the full text is in the message.
	const eventID = 1

	msg := builder.String()

	var err error

	switch {
	case record.Level >= slog.LevelError:
		err = h.log.Error(eventID, msg)
	case record.Level >= slog.LevelWarn:
		err = h.log.Warning(eventID, msg)
	default:
		err = h.log.Info(eventID, msg)
	}

	if err != nil {
		return fmt.Errorf("failed to write to event log: %w", err)
	}

	return nil
}

func writeAttr(builder *strings.Builder, attr slog.Attr) {
	if attr.Equal(slog.Attr{}) {
		return
	}

	builder.WriteString(" ")
	builder.WriteString(attr.Key)
	builder.WriteString("=")
	builder.WriteString(attr.Value.String())
}

func (h *eventLogHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	combined := make([]slog.Attr, 0, len(h.attrs)+len(attrs))
	combined = append(combined, h.attrs...)
	combined = append(combined, attrs...)

	return &eventLogHandler{log: h.log, attrs: combined}
}

func (h *eventLogHandler) WithGroup(_ string) slog.Handler {
	return h
}
