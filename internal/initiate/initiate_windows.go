//go:build windows
// +build windows

// Package initiate This package allows us to initiate Time Sensitive components (Like registering the windows service) as early as possible in the startup process
package initiate

import (
	"fmt"
	"os"

	"github.com/go-kit/log"
	"github.com/go-kit/log/level"
	"golang.org/x/sys/windows/svc"
)

const (
	serviceName = "nvidia_gpu_exporter"
)

var logger = log.NewLogfmtLogger(os.Stdout)

type windowsExporterService struct {
	stopCh chan<- bool
}

func (s *windowsExporterService) Execute(args []string, r <-chan svc.ChangeRequest, changes chan<- svc.Status) (ssec bool, errno uint32) {
	const cmdsAccepted = svc.AcceptStop | svc.AcceptShutdown
	changes <- svc.Status{State: svc.StartPending}
	changes <- svc.Status{State: svc.Running, Accepts: cmdsAccepted}
loop:
	for {
		select {
		case c := <-r:
			switch c.Cmd {
			case svc.Interrogate:
				changes <- c.CurrentStatus
			case svc.Stop, svc.Shutdown:
				level.Debug(logger).Log("msg", "Service Stop Received")
				s.stopCh <- true
				break loop
			default:
				level.Error(logger).Log("msg", fmt.Sprintf("unexpected control request #%d", c))
			}
		}
	}
	changes <- svc.Status{State: svc.StopPending}
	return
}

func init() {
	level.Debug(logger).Log("msg", "Checking if We are a service")
	isService, err := svc.IsWindowsService()
	if err != nil {
		level.Error(logger).Log("msg", err)
	}
	level.Debug(logger).Log("msg", "Attempting to start exporter service")
	if isService {
		go func() {
			err = svc.Run(serviceName, &windowsExporterService{stopCh: StopCh})
			if err != nil {
				level.Error(logger).Log("msg", "Failed to start service: ", "error", err)
			}
		}()
	}
}
