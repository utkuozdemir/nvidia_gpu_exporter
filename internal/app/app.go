// Package app wires up and runs the exporter: flag parsing, collection setup,
// metric registration and the HTTP server. It is the single entry behind the
// command-line binary and the in-process integration tests.
package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/pprof"
	"time"

	"github.com/alecthomas/kingpin/v2"
	"github.com/coreos/go-systemd/v22/activation"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	clientversion "github.com/prometheus/client_golang/prometheus/collectors/version"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/prometheus/common/promslog"
	"github.com/prometheus/common/promslog/flag"
	"github.com/prometheus/common/version"
	"github.com/prometheus/exporter-toolkit/web"
	webflag "github.com/prometheus/exporter-toolkit/web/kingpinflag"
	"golang.org/x/sync/errgroup"

	"github.com/utkuozdemir/nvidia_gpu_exporter/internal/collect"
	"github.com/utkuozdemir/nvidia_gpu_exporter/internal/exporter"
	"github.com/utkuozdemir/nvidia_gpu_exporter/internal/nvidiasmi"
)

const appName = "nvidia_gpu_exporter"

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

// Options carries what the callers inject into a run beyond the command-line
// arguments.
type Options struct {
	// ExtraLogHandler mirrors log records into an additional sink. The
	// Windows service uses it to tee logs into the Windows event log.
	ExtraLogHandler slog.Handler
	// SetDefaultLogger installs the configured logger as the process-wide
	// default. The binary sets this; in-process test runs leave it off so
	// concurrent runs do not race on global state.
	SetDefaultLogger bool
	// OnListen, when set, is called with the bound listener addresses right
	// before serving starts. Tests use it to discover the ephemeral port.
	OnListen func([]net.Addr)
}

// Run wires up the exporter from the given command-line arguments and serves
// metrics until ctx is cancelled.
//
//nolint:funlen
func Run(ctx context.Context, args []string, opts Options) error {
	app := kingpin.New(appName, "")

	var (
		webConfig = webflag.AddFlags(app, ":9835")
		network   = app.Flag("web.network",
			"Network type. Valid values are tcp4, tcp6 or tcp (for listening on both stacks).").
			Default("tcp").String()
		readTimeout = app.Flag("web.read-timeout",
			"Maximum duration before timing out read of the request.").
			Default("10s").Duration()
		readHeaderTimeout = app.Flag("web.read-header-timeout",
			"Maximum duration before timing out read of the request headers.").
			Default("10s").Duration()
		writeTimeout = app.Flag("web.write-timeout",
			"Maximum duration before timing out write of the response.").
			Default("15s").Duration()
		idleTimeout = app.Flag("web.idle-timeout",
			"Maximum amount of time to wait for the next request when keep-alive is enabled.").
			Default("60s").Duration()
		metricsPath = app.Flag("web.telemetry-path", "Path under which to expose metrics.").
				Default("/metrics").String()
		nvidiaSmiCommand = app.Flag("nvidia-smi-command",
			"Path or command to be used for the nvidia-smi executable. "+
				"Multiple words run the first as the executable with the rest as its arguments "+
				"(e.g. `sudo nvidia-smi` or an ssh wrapper). A path containing spaces must be "+
				"quoted, and the quotes must be part of this value itself, not consumed by the "+
				"shell you set the flag from: --nvidia-smi-command '\"C:\\Program Files\\...\\nvidia-smi.exe\"'.").
			Default(nvidiasmi.DefaultCommand).String()
		qFields = app.Flag("query-field-names",
			fmt.Sprintf("Comma-separated list of the query fields. "+
				"You can find out possible fields by running `nvidia-smi --help-query-gpu`. "+
				"The value `%s` will automatically detect the fields to query.", nvidiasmi.DefaultQField)).
			Default(nvidiasmi.DefaultQField).String()
		qFieldsExclude = app.Flag("query-field-names-exclude",
			"Comma-separated list of query fields to exclude from being queried. "+
				"Names match literally, with `*` as a wildcard for any sequence of characters "+
				"(for example `remapped_rows.histogram.*`). Useful to drop fields that are slow "+
				"or unsupported on a given setup.").
			Default("").String()
		collectInterval = app.Flag("collect.interval",
			"Interval at which nvidia-smi runs in the background, with scrapes serving the most "+
				"recent result. When 0, nvidia-smi runs synchronously on each scrape instead.").
			Default("0").Duration()
		collectTimeout = app.Flag("collect.timeout",
			"Maximum duration a single collection cycle may take, including all nvidia-smi runs "+
				"within it and the runs at startup. 0 disables the bound.").
			Default("10s").Duration()
		collectComputeApps = app.Flag("collect.compute-apps",
			"Also export per-process GPU metrics from `nvidia-smi --query-compute-apps`. "+
				"Adds one nvidia-smi run per collection cycle. When the exporter runs in a "+
				"container, seeing other workloads' processes requires sharing the host PID "+
				"namespace (hostPID in Kubernetes, --pid=host in Docker).").
			Default("false").Bool()
		shutdownOnErr = app.Flag("shutdown-on-error",
			"Shut down the exporter if there is an error querying nvidia-smi. "+
				"When false, exporter will simply log this error and export it as a metric, but will not crash.").
			Default("false").Bool()
		enablePprof = app.Flag("web.enable-pprof",
			"Enable pprof endpoints for profiling under /debug/pprof/. "+
				"Only enable this on a trusted network, as it exposes runtime internals.").
			Default("false").Bool()
	)

	promSlogConfig := &promslog.Config{}

	flag.AddFlags(app, promSlogConfig)
	app.Version(version.Print(appName))
	app.HelpFlag.Short('h')

	if _, err := app.Parse(args); err != nil {
		return fmt.Errorf("failed to parse the command line: %w", err)
	}

	logger := buildLogger(promSlogConfig, opts.ExtraLogHandler)

	if opts.SetDefaultLogger {
		slog.SetDefault(logger)
	}

	if err := validateCollectFlags(*collectInterval, *collectTimeout); err != nil {
		return err
	}

	ctx, serverCancel := context.WithCancelCause(ctx)
	defer serverCancel(nil)

	var onFatal func(error)
	if *shutdownOnErr {
		onFatal = serverCancel
	}

	eg, ctx := errgroup.WithContext(ctx)

	collectCfg := collectConfig{
		nvidiaSmiCommand: *nvidiaSmiCommand,
		qFieldsRaw:       *qFields,
		qFieldsExclude:   *qFieldsExclude,
		interval:         *collectInterval,
		timeout:          *collectTimeout,
		computeApps:      *collectComputeApps,
		onFatal:          onFatal,
	}

	registry := prometheus.NewRegistry()

	err := setupExporter(ctx, eg, collectCfg, registry, logger)
	if err != nil {
		return err
	}

	mux := newServeMux(logger, *metricsPath, *enablePprof, registry)

	srv := &http.Server{
		ReadHeaderTimeout: *readHeaderTimeout,
		ReadTimeout:       *readTimeout,
		WriteTimeout:      *writeTimeout,
		IdleTimeout:       *idleTimeout,
		Handler:           mux,
	}

	serveHTTP(ctx, eg, srv, webConfig, *network, opts.OnListen, logger)

	if err = eg.Wait(); err != nil {
		return fmt.Errorf("failed to run: %w", err)
	}

	return nil
}

// serveHTTP adds the serving goroutine and its shutdown companion to the
// errgroup.
func serveHTTP(
	ctx context.Context,
	eg *errgroup.Group,
	srv *http.Server,
	webConfig *web.FlagConfig,
	network string,
	onListen func([]net.Addr),
	logger *slog.Logger,
) {
	eg.Go(func() error {
		<-ctx.Done()

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
		if runErr := listenAndServe(ctx, srv, webConfig, network, onListen, logger); runErr != nil {
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
}

// validateCollectFlags rejects the flag values kingpin's types cannot.
func validateCollectFlags(interval, timeout time.Duration) error {
	if interval < 0 {
		return fmt.Errorf("collect.interval must not be negative, got %s", interval)
	}

	if timeout < 0 {
		return fmt.Errorf("collect.timeout must not be negative, got %s", timeout)
	}

	return nil
}

// collectConfig carries the collection-related settings from the flags to the
// exporter setup.
type collectConfig struct {
	nvidiaSmiCommand string
	qFieldsRaw       string
	qFieldsExclude   string
	interval         time.Duration
	timeout          time.Duration
	computeApps      bool
	onFatal          func(error)
}

// setupExporter resolves the query fields, builds the collection source
// (adding the background collector to the errgroup when an interval is set),
// and registers the exporter on the given registry, along with the collectors
// the default registry would carry.
func setupExporter(
	ctx context.Context,
	eg *errgroup.Group,
	cfg collectConfig,
	registry *prometheus.Registry,
	logger *slog.Logger,
) error {
	resolved, err := nvidiasmi.ResolveFields(
		ctx,
		cfg.nvidiaSmiCommand,
		cfg.qFieldsRaw,
		cfg.qFieldsExclude,
		cfg.timeout,
		nvidiasmi.DefaultRunFunc,
		logger,
	)
	if err != nil {
		return fmt.Errorf("failed to resolve query fields: %w", err)
	}

	query := buildQueryFunc(cfg, resolved, logger)

	var src collect.Source

	switch {
	case cfg.interval > 0:
		cached := collect.NewCached(query, cfg.interval, cfg.timeout, cfg.onFatal, logger)

		eg.Go(func() error { return cached.Run(ctx) })

		src = cached
	default:
		src = collect.NewLive(query, cfg.timeout, cfg.onFatal, logger)
	}

	exp := exporter.New(ctx, exporter.DefaultPrefix, resolved, src, cfg.computeApps, logger)

	// the go and process collectors keep the exposed families identical to
	// what the default registry used to serve
	for _, collector := range []prometheus.Collector{
		exp,
		clientversion.NewCollector(appName),
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
	} {
		if err = registry.Register(collector); err != nil {
			return fmt.Errorf("failed to register collector: %w", err)
		}
	}

	return nil
}

// buildQueryFunc builds the collection cycle: the GPU query, plus the
// per-process query when enabled. The returned error and exit code describe
// the GPU query alone; the per-process query fails softly inside the Reading,
// per the contract on collect.QueryFunc.
func buildQueryFunc(
	cfg collectConfig,
	resolved nvidiasmi.ResolvedFields,
	logger *slog.Logger,
) collect.QueryFunc {
	return func(queryCtx context.Context) (collect.Reading, int, error) {
		table, exitCode, err := nvidiasmi.Query(
			queryCtx, cfg.nvidiaSmiCommand, resolved.Query, nvidiasmi.DefaultRunFunc)
		if err != nil {
			return collect.Reading{}, exitCode, fmt.Errorf("failed to query gpus: %w", err)
		}

		reading := collect.Reading{Table: table}

		if cfg.computeApps {
			reading.AppsAttempted = true

			apps, appsErr := nvidiasmi.QueryComputeApps(
				queryCtx, cfg.nvidiaSmiCommand, nvidiasmi.DefaultRunFunc, logger)
			if appsErr != nil {
				reading.AppsErr = appsErr
			} else {
				reading.Apps = apps
				reading.AppsSuccess = true
			}
		}

		return reading, exitCode, nil
	}
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

func (r *RootHandler) ServeHTTP(w http.ResponseWriter, _ *http.Request) {
	if _, err := w.Write(r.response); err != nil {
		r.logger.Error("failed to write redirect", "err", err)
	}
}

// newServeMux builds the HTTP mux serving the root page, metrics and
// optionally pprof. The metrics handler is instrumented the same way the
// default promhttp handler is, so the promhttp_* families stay exposed.
func newServeMux(
	logger *slog.Logger,
	metricsPath string,
	enablePprof bool,
	registry *prometheus.Registry,
) *http.ServeMux {
	mux := http.NewServeMux()

	rootHandler := NewRootHandler(logger, metricsPath, enablePprof)
	mux.Handle("GET /", rootHandler)
	mux.Handle("GET "+metricsPath,
		promhttp.InstrumentMetricHandler(registry, promhttp.HandlerFor(registry, promhttp.HandlerOpts{})))

	if enablePprof {
		logger.Info("pprof endpoints enabled")
		registerPprof(mux)
	}

	return mux
}

// registerPprof wires up the net/http/pprof handlers on the given mux.
func registerPprof(mux *http.ServeMux) {
	mux.HandleFunc("GET /debug/pprof/", pprof.Index)
	mux.HandleFunc("GET /debug/pprof/cmdline", pprof.Cmdline)
	mux.HandleFunc("GET /debug/pprof/profile", pprof.Profile)
	mux.HandleFunc("GET /debug/pprof/symbol", pprof.Symbol)
	mux.HandleFunc("GET /debug/pprof/trace", pprof.Trace)
}

// buildLogger creates the application logger, optionally tee'ing records into
// an additional handler. The extra handler mirrors logs into the Windows event
// log when running as a service.
func buildLogger(cfg *promslog.Config, extraHandler slog.Handler) *slog.Logger {
	logger := promslog.New(cfg)
	if extraHandler == nil {
		return logger
	}

	// Mirror records into the extra handler (the Windows event log) at the same
	// level as the primary logger, so --log.level applies to both sinks instead
	// of the event log keeping its own fixed level.
	mirrored := &leveledHandler{leveler: cfg.Level, handler: extraHandler}

	return slog.New(newMultiHandler(logger.Handler(), mirrored))
}

// leveledHandler gates an inner handler at a shared level. It lets a tee'd sink
// follow a configured level (the same Leveler the primary logger uses) rather
// than deciding its own.
type leveledHandler struct {
	leveler slog.Leveler
	handler slog.Handler
}

func (h *leveledHandler) Enabled(_ context.Context, level slog.Level) bool {
	return level >= h.leveler.Level()
}

func (h *leveledHandler) Handle(ctx context.Context, record slog.Record) error {
	//nolint:wrapcheck // delegates to the wrapped handler, which wraps its own errors.
	return h.handler.Handle(ctx, record)
}

func (h *leveledHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &leveledHandler{leveler: h.leveler, handler: h.handler.WithAttrs(attrs)}
}

func (h *leveledHandler) WithGroup(name string) slog.Handler {
	return &leveledHandler{leveler: h.leveler, handler: h.handler.WithGroup(name)}
}

// multiHandler fans a slog record out to several handlers. It lets the exporter
// keep logging to its configured output while also mirroring records into the
// Windows event log when running as a service.
type multiHandler struct {
	handlers []slog.Handler
}

func newMultiHandler(handlers ...slog.Handler) *multiHandler {
	return &multiHandler{handlers: handlers}
}

func (m *multiHandler) Enabled(ctx context.Context, level slog.Level) bool {
	for _, h := range m.handlers {
		if h.Enabled(ctx, level) {
			return true
		}
	}

	return false
}

func (m *multiHandler) Handle(ctx context.Context, record slog.Record) error {
	var err error

	for _, h := range m.handlers {
		if !h.Enabled(ctx, record.Level) {
			continue
		}

		if handleErr := h.Handle(ctx, record.Clone()); handleErr != nil {
			err = errors.Join(err, handleErr)
		}
	}

	return err
}

func (m *multiHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	handlers := make([]slog.Handler, len(m.handlers))
	for i, h := range m.handlers {
		handlers[i] = h.WithAttrs(attrs)
	}

	return &multiHandler{handlers: handlers}
}

func (m *multiHandler) WithGroup(name string) slog.Handler {
	handlers := make([]slog.Handler, len(m.handlers))
	for i, h := range m.handlers {
		handlers[i] = h.WithGroup(name)
	}

	return &multiHandler{handlers: handlers}
}

// listenAndServe is the same as web.ListenAndServe but supports passing the
// network stack as an argument and reporting the bound addresses.
func listenAndServe(
	ctx context.Context,
	server *http.Server,
	flags *web.FlagConfig,
	network string,
	onListen func([]net.Addr),
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

		notifyListen(listeners, onListen)

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

	notifyListen(listeners, onListen)

	if err := web.ServeMultiple(listeners, server, flags, logger); err != nil {
		return fmt.Errorf("failed to serve: %w", err)
	}

	return nil
}

// notifyListen reports the bound listener addresses to the onListen hook.
func notifyListen(listeners []net.Listener, onListen func([]net.Addr)) {
	if onListen == nil {
		return
	}

	addrs := make([]net.Addr, 0, len(listeners))
	for _, listener := range listeners {
		addrs = append(addrs, listener.Addr())
	}

	onListen(addrs)
}
