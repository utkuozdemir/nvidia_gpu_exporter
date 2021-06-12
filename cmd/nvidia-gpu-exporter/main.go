package main

import (
	"flag"
	"fmt"
	"github.com/go-kit/log"
	"github.com/go-kit/log/level"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/prometheus/common/promlog"
	"github.com/prometheus/exporter-toolkit/web"
	"net/http"
	"nvidia-smi-exporter/internal/exporter"
	"os"
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
	var nvidiaSmiCommand string
	flag.StringVar(&nvidiaSmiCommand, "nvidia-smi-command", exporter.DefaultNvidiaSmiCommand,
		"The path or command to be used for the nvidia-smi executable")
	flag.Parse()

	logConfig := promlog.Config{}
	logger := promlog.New(&logConfig)

	e, err := exporter.New(exporter.DefaultPrefix, nvidiaSmiCommand, exporter.DefaultQueryFieldNames, logger)
	if err != nil {
		_ = level.Error(logger).Log("msg", "Error on creating exporter", "err", err)
		os.Exit(1)
	}

	prometheus.MustRegister(e)

	metricsPath := "/metrics"
	listenAddress := ":9000"
	_ = level.Info(logger).Log("msg", "Listening on address", "address", listenAddress)

	rootHandler := NewRootHandler(logger, metricsPath)
	http.Handle("/", rootHandler)
	http.Handle(metricsPath, promhttp.Handler())

	srv := &http.Server{Addr: listenAddress}
	if err := web.ListenAndServe(srv, "", logger); err != nil {
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
	_, err := w.Write(r.response)
	if err != nil {
		_ = level.Error(r.logger).Log("Error writing redirect", "err", err)
	}
}
