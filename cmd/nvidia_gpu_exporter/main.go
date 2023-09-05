package main

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/alecthomas/kingpin/v2"
	"github.com/coreos/go-systemd/v22/activation"
	"github.com/go-kit/log"
	"github.com/go-kit/log/level"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/prometheus/common/promlog"
	"github.com/prometheus/common/promlog/flag"
	"github.com/prometheus/common/version"
	"github.com/prometheus/exporter-toolkit/web"
	webflag "github.com/prometheus/exporter-toolkit/web/kingpinflag"

	"github.com/utkuozdemir/nvidia_gpu_exporter/internal/exporter"
	"github.com/utkuozdemir/nvidia_gpu_exporter/internal/initiate"
)

const (
	redirectPageTemplate = `<html lang="en">
<head><title>Nvidia GPU Exporter</title></head>
<body>
<h1>Nvidia GPU Exporter</h1>
<p><a href="%s">Metrics</a></p>
</body>
</html>
`
)

// main is the entrypoint of the application.
//
//nolint:funlen
func main() {
	var (
		webConfig = webflag.AddFlags(kingpin.CommandLine, ":9835")
		network   = kingpin.Flag("web.network",
			"Network type. Valid values are tcp4, tcp6 or tcp (for listening on both stacks).").
			Default("tcp").String()
		readTimeout = kingpin.Flag("web.read-timeout",
			"Maximum duration before timing out read of the request.").
			Default("10s").Duration()
		readHeaderTimeout = kingpin.Flag("web.read-header-timeout",
			"Maximum duration before timing out read of the request headers.").
			Default("10s").Duration()
		writeTimeout = kingpin.Flag("web.write-timeout",
			"Maximum duration before timing out write of the response.").
			Default("15s").Duration()
		idleTimeout = kingpin.Flag("web.idle-timeout",
			"Maximum amount of time to wait for the next request when keep-alive is enabled.").
			Default("60s").Duration()
		metricsPath = kingpin.Flag("web.telemetry-path", "Path under which to expose metrics.").
				Default("/metrics").String()
		nvidiaSmiCommand = kingpin.Flag("nvidia-smi-command",
			"Path or command to be used for the nvidia-smi executable").
			Default(exporter.DefaultNvidiaSmiCommand).String()
		qFields = kingpin.Flag("query-field-names",
			fmt.Sprintf("Comma-separated list of the query fields. "+
				"You can find out possible fields by running `nvidia-smi --help-query-gpus`. "+
				"The value `%s` will automatically detect the fields to query.", exporter.DefaultQField)).
			Default(exporter.DefaultQField).String()
	)

	promlogConfig := &promlog.Config{}
	flag.AddFlags(kingpin.CommandLine, promlogConfig)
	kingpin.Version(version.Print("nvidia_gpu_exporter"))
	kingpin.HelpFlag.Short('h')
	kingpin.Parse()

	logger := promlog.New(promlogConfig)

	exp, err := exporter.New(exporter.DefaultPrefix, *nvidiaSmiCommand, *qFields, logger)
	if err != nil {
		_ = level.Error(logger).Log("msg", "Error on creating exporter", "err", err)

		os.Exit(1)
	}

	prometheus.MustRegister(exp)
	prometheus.MustRegister(version.NewCollector("nvidia_gpu_exporter"))

	rootHandler := NewRootHandler(logger, *metricsPath)
	http.Handle("/", rootHandler)
	http.Handle(*metricsPath, promhttp.Handler())

	srv := &http.Server{
		ReadHeaderTimeout: *readHeaderTimeout,
		ReadTimeout:       *readTimeout,
		WriteTimeout:      *writeTimeout,
		IdleTimeout:       *idleTimeout,
	}

	go func() {
		if err := listenAndServe(srv, webConfig, *network, logger); err != nil {
			_ = level.Error(logger).Log("msg", "Error starting HTTP server", "err", err)

			os.Exit(1)
		}
	}()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT)

	select {
	case <-initiate.StopCh:
		_ = level.Info(logger).Log("msg", "Shutting down from service")
	case <-sig:
		_ = level.Info(logger).Log("msg", "Shutting down from signal")
	}

	os.Exit(0)
}

type RootHandler struct {
	response []byte
	logger   log.Logger
}

func NewRootHandler(logger log.Logger, metricsPath string) *RootHandler {
	return &RootHandler{
		response: []byte(fmt.Sprintf(redirectPageTemplate, metricsPath)),
		logger:   logger,
	}
}

func (r *RootHandler) ServeHTTP(w http.ResponseWriter, _ *http.Request) {
	if _, err := w.Write(r.response); err != nil {
		_ = level.Error(r.logger).Log("msg", "Error writing redirect", "err", err)
	}
}

// listenAndServe is same as web.ListenAndServe but supports passing network stack as an argument.
//
//nolint:all
func listenAndServe(server *http.Server, flags *web.FlagConfig, network string, logger log.Logger) error {
	if *flags.WebSystemdSocket {
		level.Info(logger).Log("msg", "Listening on systemd activated listeners instead of port listeners.")
		listeners, err := activation.Listeners()
		if err != nil {
			return err
		}
		if len(listeners) < 1 {
			return errors.New("no socket activation file descriptors found")
		}
		return web.ServeMultiple(listeners, server, flags, logger)
	}
	listeners := make([]net.Listener, 0, len(*flags.WebListenAddresses))
	for _, address := range *flags.WebListenAddresses {
		listener, err := net.Listen(network, address)
		if err != nil {
			return err
		}
		defer listener.Close()
		listeners = append(listeners, listener)
	}
	return web.ServeMultiple(listeners, server, flags, logger)
}
