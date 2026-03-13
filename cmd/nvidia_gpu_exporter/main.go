package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/pprof"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"time"

	"github.com/alecthomas/kingpin/v2"
	"github.com/coreos/go-systemd/v22/activation"
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

// main is the entrypoint of the application.
func main() {
	if err := run(); err != nil {
		slog.Default().Error("failed to run", "err", err)

		var exitErr *exec.ExitError

		if errors.As(err, &exitErr) {
			os.Exit(exitErr.ExitCode())
		}

		os.Exit(1)
	}
}

//nolint:funlen
func run() error {
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
		shutdownOnErr = kingpin.Flag("shutdown-on-error",
			"Shut down the exporter if there is an error querying nvidia-smi. "+
				"When false, exporter will simply log this error and export it as a metric, but will not crash.").
			Default("false").Bool()
		enablePprof = kingpin.Flag("web.enable-pprof",
			"Enable pprof endpoints for profiling.").
			Default("true").Bool()
	)

	promSlogConfig := &promslog.Config{}

	flag.AddFlags(kingpin.CommandLine, promSlogConfig)
	kingpin.Version(version.Print("nvidia_gpu_exporter"))
	kingpin.HelpFlag.Short('h')
	kingpin.Parse()

	logger := promslog.New(promSlogConfig)

	slog.SetDefault(logger)

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	ctx, serverCancel := context.WithCancelCause(ctx)
	defer serverCancel(nil)

	var shutdownOnErrFunc context.CancelCauseFunc
	if *shutdownOnErr {
		shutdownOnErrFunc = serverCancel
	}

	exp, err := exporter.New(
		ctx,
		shutdownOnErrFunc,
		exporter.DefaultPrefix,
		*nvidiaSmiCommand,
		*qFields,
		logger,
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

	if *enablePprof {
		logger.Info("pprof endpoints enabled")
	}

	mux := http.NewServeMux()

	rootHandler := NewRootHandler(logger, *metricsPath, *enablePprof)
	mux.Handle("GET /", rootHandler)
	mux.Handle("GET " + *metricsPath, promhttp.Handler())

	if *enablePprof {
		mux.HandleFunc("GET /debug/pprof/", pprof.Index)
		mux.HandleFunc("GET /debug/pprof/cmdline", pprof.Cmdline)
		mux.HandleFunc("GET /debug/pprof/profile", pprof.Profile)
		mux.HandleFunc("GET /debug/pprof/symbol", pprof.Symbol)
		mux.HandleFunc("GET /debug/pprof/trace", pprof.Trace)
	}

	srv := &http.Server{
		ReadHeaderTimeout: *readHeaderTimeout,
		ReadTimeout:       *readTimeout,
		WriteTimeout:      *writeTimeout,
		IdleTimeout:       *idleTimeout,
		Handler:           mux,
	}

	eg, ctx := errgroup.WithContext(ctx) //nolint:varnamelen

	eg.Go(func() error {
		<-ctx.Done()

		//nolint:mnd
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutdownCancel()

		logger.Info("shutting down http server")

		//nolint:contextcheck
		if shutdownErr := srv.Shutdown(shutdownCtx); shutdownErr != nil {
			return fmt.Errorf("failed to shutdown http server: %w", shutdownErr)
		}

		return nil
	})

	eg.Go(func() error {
		if runErr := listenAndServe(ctx, srv, webConfig, *network, logger); runErr != nil {
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
		response: []byte(fmt.Sprintf(redirectPageTemplate, metricsPath, pprofLinks)),
		logger:   logger,
	}
}

func (r *RootHandler) ServeHTTP(w http.ResponseWriter, _ *http.Request) {
	if _, err := w.Write(r.response); err != nil {
		r.logger.Error("failed to write redirect", "err", err)
	}
}

// listenAndServe is the same as web.ListenAndServe but supports passing network stack as an argument.
func listenAndServe(
	ctx context.Context,
	server *http.Server,
	flags *web.FlagConfig,
	network string,
	logger *slog.Logger,
) (retErr error) {
	if *flags.WebSystemdSocket {
		logger.Info("listening on systemd activated listeners instead of port listeners")

		listeners, err := activation.Listeners()
		if err != nil {
			return fmt.Errorf("failed to get activation listeners: %w", err)
		}

		if len(listeners) < 1 {
			return errors.New("no socket activation file descriptors found")
		}

		if err = web.ServeMultiple(listeners, server, flags, logger); err != nil {
			return fmt.Errorf("failed to serve: %w", err)
		}

		return nil
	}

	listeners := make([]net.Listener, 0, len(*flags.WebListenAddresses))

	for _, address := range *flags.WebListenAddresses {
		var lc net.ListenConfig

		listener, err := lc.Listen(ctx, network, address)
		if err != nil {
			return fmt.Errorf("failed to listen on %s: %w", address, err)
		}

		listeners = append(listeners, listener)
	}

	defer func() {
		for _, listener := range listeners {
			if err := listener.Close(); err != nil {
				retErr = errors.Join(retErr, fmt.Errorf("failed to close listener: %w", err))
			}
		}
	}()

	if err := web.ServeMultiple(listeners, server, flags, logger); err != nil {
		return fmt.Errorf("failed to serve: %w", err)
	}

	return nil
}
