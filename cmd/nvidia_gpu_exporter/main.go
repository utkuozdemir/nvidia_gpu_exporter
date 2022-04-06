package main

import (
	"fmt"
	"net/http"
	"os"

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
	"gopkg.in/alecthomas/kingpin.v2"
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

func main() {
	var (
		webConfig     = webflag.AddFlags(kingpin.CommandLine)
		listenAddress = kingpin.Flag("web.listen-address",
			"Address to listen on for web interface and telemetry.").
			Default(":9835").String()
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

	_ = level.Info(logger).Log("msg", "Listening on address", "address", listenAddress)

	rootHandler := NewRootHandler(logger, *metricsPath)
	http.Handle("/", rootHandler)
	http.Handle(*metricsPath, promhttp.Handler())

	srv := &http.Server{Addr: *listenAddress}
	if err := web.ListenAndServe(srv, *webConfig, logger); err != nil {
		_ = level.Error(logger).Log("msg", "Error starting HTTP server", "err", err)

		os.Exit(1)
	}
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
