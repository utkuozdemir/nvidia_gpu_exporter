package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/pprof"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/alecthomas/kingpin/v2"
	"github.com/kardianos/service"
	"github.com/prometheus/client_golang/prometheus"
	clientversion "github.com/prometheus/client_golang/prometheus/collectors/version"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/prometheus/common/promslog"
	"github.com/prometheus/common/promslog/flag"
	"github.com/prometheus/common/version"
	"github.com/prometheus/exporter-toolkit/web"
	webflag "github.com/prometheus/exporter-toolkit/web/kingpinflag"
	"golang.org/x/sync/errgroup"

	"github.com/utkuozdemir/nvidia_gpu_exporter/internal/exporter"
)

const redirectPageTemplate = `<html lang="en">
<head><title>Nvidia GPU Exporter</title></head>
<body>
<h1>Nvidia GPU Exporter</h1>
<p><a href="%s">Metrics</a></p>%s
</body>
</html>
`

const pprofLinksHTML = `
<h2>Profiling</h2>
<ul>
<li><a href="/debug/pprof/">Index</a></li>
<li><a href="/debug/pprof/goroutine">Goroutines</a></li>
<li><a href="/debug/pprof/heap">Heap</a></li>
<li><a href="/debug/pprof/threadcreate">Threads</a></li>
<li><a href="/debug/pprof/block">Block</a></li>
<li><a href="/debug/pprof/mutex">Mutex</a></li>
<li><a href="/debug/pprof/profile">CPU Profile</a></li>
<li><a href="/debug/pprof/trace">Trace</a></li>
</ul>
`

type program struct {
	webConfig         *web.FlagConfig
	network           string
	readTimeout       time.Duration
	readHeaderTimeout time.Duration
	writeTimeout      time.Duration
	idleTimeout       time.Duration
	metricsPath       string
	nvidiaSmiCommand  string
	qFields           string
	qFieldsExclude    string
	shutdownOnErr     bool
	enablePprof       bool
	logger            *slog.Logger

	cancel context.CancelCauseFunc
	done   chan error
}

func (p *program) Start(_ service.Service) error {
	ctx, cancel := context.WithCancelCause(context.Background())
	p.cancel = cancel
	p.done = make(chan error, 1)

	go func() {
		p.done <- p.run(ctx)
	}()

	return nil
}

func (p *program) Stop(_ service.Service) error {
	p.cancel(nil)
	return <-p.done
}

//nolint:funlen
func (p *program) run(ctx context.Context) error {
	var shutdownOnErrFunc context.CancelCauseFunc
	if p.shutdownOnErr {
		shutdownOnErrFunc = p.cancel
	}

	exp, err := exporter.New(
		ctx,
		shutdownOnErrFunc,
		exporter.DefaultPrefix,
		p.nvidiaSmiCommand,
		p.qFields,
		p.qFieldsExclude,
		p.logger,
	)
	if err != nil {
		return fmt.Errorf("failed to create exporter: %w", err)
	}

	if err = prometheus.Register(exp); err != nil {
		return fmt.Errorf("failed to register exporter: %w", err)
	}

	if err = prometheus.Register(clientversion.NewCollector("nvidia_gpu_exporter")); err != nil {
		return fmt.Errorf("failed to register client version collector: %w", err)
	}

	mux := newServeMux(p.logger, p.metricsPath, p.enablePprof)

	srv := &http.Server{
		ReadHeaderTimeout: p.readHeaderTimeout,
		ReadTimeout:       p.readTimeout,
		WriteTimeout:      p.writeTimeout,
		IdleTimeout:       p.idleTimeout,
		Handler:           mux,
	}

	eg, ctx := errgroup.WithContext(ctx)

	eg.Go(func() error {
		<-ctx.Done()

		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutdownCancel()

		p.logger.Info("shutting down http server")

		//nolint:contextcheck
		if shutdownErr := srv.Shutdown(shutdownCtx); shutdownErr != nil {
			return fmt.Errorf("failed to shutdown http server: %w", shutdownErr)
		}

		return nil
	})

	eg.Go(func() error {
		if runErr := listenAndServe(ctx, srv, p.webConfig, p.network, p.logger); runErr != nil {
			if !errors.Is(runErr, http.ErrServerClosed) {
				return runErr
			}

			serverCancelCause := context.Cause(ctx)
			if errors.Is(serverCancelCause, context.Canceled) {
				return nil
			}

			return fmt.Errorf("exporter failed: %w", serverCancelCause)
		}

		return nil
	})

	if err = eg.Wait(); err != nil {
		return fmt.Errorf("failed to run: %w", err)
	}

	return nil
}

type RootHandler struct {
	response []byte
	logger   *slog.Logger
}

func NewRootHandler(logger *slog.Logger, metricsPath string, enablePprof bool) *RootHandler {
	pprofLinks := ""
	if enablePprof {
		pprofLinks = pprofLinksHTML
	}

	return &RootHandler{
		response: fmt.Appendf(nil, redirectPageTemplate, metricsPath, pprofLinks),
		logger:   logger,
	}
}

func newServeMux(logger *slog.Logger, metricsPath string, enablePprof bool) *http.ServeMux {
	mux := http.NewServeMux()

	rootHandler := NewRootHandler(logger, metricsPath, enablePprof)
	mux.Handle("GET /", rootHandler)
	mux.Handle("GET "+metricsPath, promhttp.Handler())

	if enablePprof {
		logger.Info("pprof endpoints enabled")
		registerPprof(mux)
	}

	return mux
}

func registerPprof(mux *http.ServeMux) {
	mux.HandleFunc("GET /debug/pprof/", pprof.Index)
	mux.HandleFunc("GET /debug/pprof/cmdline", pprof.Cmdline)
	mux.HandleFunc("GET /debug/pprof/profile", pprof.Profile)
	mux.HandleFunc("GET /debug/pprof/symbol", pprof.Symbol)
	mux.HandleFunc("GET /debug/pprof/trace", pprof.Trace)
}

func (r *RootHandler) ServeHTTP(w http.ResponseWriter, _ *http.Request) {
	if _, err := w.Write(r.response); err != nil {
		r.logger.Error("failed to write redirect", "err", err)
	}
}

// argsWithoutServiceFlag returns os.Args[1:] with the --service flag and its value removed,
// so that service.Config.Arguments embeds only the runtime flags when the service is installed.
func argsWithoutServiceFlag() []string {
	result := make([]string, 0, len(os.Args)-1)
	skip := false

	for _, arg := range os.Args[1:] {
		if skip {
			skip = false
			continue
		}

		if arg == "--service" || arg == "-service" {
			skip = true
			continue
		}

		if strings.HasPrefix(arg, "--service=") || strings.HasPrefix(arg, "-service=") {
			continue
		}

		result = append(result, arg)
	}

	return result
}

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
				"You can find out possible fields by running `nvidia-smi --help-query-gpu`. "+
				"The value `%s` will automatically detect the fields to query.", exporter.DefaultQField)).
			Default(exporter.DefaultQField).String()
		qFieldsExclude = kingpin.Flag("query-field-names-exclude",
			"Comma-separated list of query fields to exclude from being queried. "+
				"Names match literally, with `*` as a wildcard for any sequence of characters "+
				"(for example `remapped_rows.histogram.*`). Useful to drop fields that are slow "+
				"or unsupported on a given setup.").
			Default("").String()
		shutdownOnErr = kingpin.Flag("shutdown-on-error",
			"Shut down the exporter if there is an error querying nvidia-smi. "+
				"When false, exporter will simply log this error and export it as a metric, but will not crash.").
			Default("false").Bool()
		enablePprof = kingpin.Flag("web.enable-pprof",
			"Enable pprof endpoints for profiling under /debug/pprof/. "+
				"Only enable this on a trusted network, as it exposes runtime internals.").
			Default("false").Bool()
		svcAction = kingpin.Flag("service",
			"Control the system service. Valid actions: install, uninstall, start, stop, restart, status.").
			String()
	)

	promSlogConfig := &promslog.Config{}

	flag.AddFlags(kingpin.CommandLine, promSlogConfig)
	kingpin.Version(version.Print("nvidia_gpu_exporter"))
	kingpin.HelpFlag.Short('h')
	kingpin.Parse()

	logger := promslog.New(promSlogConfig)
	slog.SetDefault(logger)

	prog := &program{
		webConfig:         webConfig,
		network:           *network,
		readTimeout:       *readTimeout,
		readHeaderTimeout: *readHeaderTimeout,
		writeTimeout:      *writeTimeout,
		idleTimeout:       *idleTimeout,
		metricsPath:       *metricsPath,
		nvidiaSmiCommand:  *nvidiaSmiCommand,
		qFields:           *qFields,
		qFieldsExclude:    *qFieldsExclude,
		shutdownOnErr:     *shutdownOnErr,
		enablePprof:       *enablePprof,
		logger:            logger,
	}

	svcConfig := &service.Config{
		Name:        "NvidiaGPUExporter",
		DisplayName: "Nvidia GPU Exporter",
		Description: "Prometheus exporter for Nvidia GPU metrics via nvidia-smi",
		Arguments:   argsWithoutServiceFlag(),
	}

	svc, err := service.New(prog, svcConfig)
	if err != nil {
		slog.Default().Error("failed to create service", "err", err)
		os.Exit(1)
	}

	if *svcAction != "" {
		if err = service.Control(svc, *svcAction); err != nil {
			slog.Default().Error("failed to control service", "action", *svcAction, "err", err)
			os.Exit(1)
		}

		return
	}

	if err = svc.Run(); err != nil {
		slog.Default().Error("failed to run", "err", err)

		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			os.Exit(exitErr.ExitCode())
		}

		os.Exit(1)
	}
}
