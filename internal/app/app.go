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
	"path"
	"strconv"
	"strings"
	"time"
	"unicode"

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
	"github.com/utkuozdemir/nvidia_gpu_exporter/internal/nvmlnative"
)

const appName = "nvidia_gpu_exporter"

// scrapeTimeoutHeader is set by Prometheus on every scrape to advertise the
// timeout it applies to it.
const scrapeTimeoutHeader = "X-Prometheus-Scrape-Timeout-Seconds"

// maxScrapeTimeoutSeconds caps the advertised scrape timeout the exporter
// honors. Values beyond it are nonsensical (and would overflow the duration
// conversion), so they are treated like a missing header.
const maxScrapeTimeoutSeconds = 24 * 60 * 60

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
	// Terminate overrides what --help and --version do after printing: the
	// flag parser terminates the process by default, which is exactly what
	// the binary wants, but would kill the whole test process for in-process
	// callers that pass such flags through.
	Terminate func(int)
}

// Run wires up the exporter from the given command-line arguments and serves
// metrics until ctx is cancelled.
//
//nolint:funlen,cyclop
func Run(ctx context.Context, args []string, opts Options) error {
	app := kingpin.New(appName, "")

	if opts.Terminate != nil {
		app.Terminate(opts.Terminate)
	}

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
		maxRequests = app.Flag("web.max-requests",
			"Maximum number of concurrent scrapes of the metrics endpoint. Requests beyond "+
				"the limit are answered with a 503 immediately instead of queueing up behind "+
				"a slow collection. 0 disables the limit.").
			Default("40").Int()
		timeoutOffset = app.Flag("web.timeout-offset",
			"Offset subtracted from the scrape timeout Prometheus advertises in the "+
				scrapeTimeoutHeader+" header, leaving time for the response to reach "+
				"Prometheus. The advertised timeout minus this offset bounds each scrape's "+
				"collection, on top of --collect.timeout.").
			Default("500ms").Duration()
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
		collectBackend = app.Flag("collect.backend",
			"How to collect GPU metrics. `exec` runs nvidia-smi (the default); `nvml` is "+
				"experimental and reads the driver library (libnvidia-ml) directly, without "+
				"nvidia-smi. The nvml backend requires Linux and a build with the backend "+
				"compiled in. It exposes every metric the exec backend exposes, plus "+
				"NVML-only extras (see the docs).").
			Default(DefaultBackend).Enum("exec", "nvml")
		collectInterval = app.Flag("collect.interval",
			"Interval at which the collection runs in the background, with scrapes serving "+
				"the most recent result. When 0, the collection runs synchronously on each "+
				"scrape instead.").
			Default("0").Duration()
		collectTimeout = app.Flag("collect.timeout",
			"Maximum duration a single collection cycle may take, including all the work "+
				"within it (e.g. the nvidia-smi runs) and the runs at startup. 0 disables "+
				"the bound.").
			Default("10s").Duration()
		collectComputeApps = app.Flag("collect.compute-apps",
			"Also export per-process GPU metrics (from `nvidia-smi --query-compute-apps`, "+
				"or the equivalent NVML calls in nvml mode). When the exporter runs in a "+
				"container, seeing other workloads' processes requires sharing the host PID "+
				"namespace (hostPID in Kubernetes, --pid=host in Docker).").
			Default("false").Bool()
		collectComputeAppsMIG = app.Flag("collect.compute-apps-mig",
			"Add MIG attribution labels (gpu_instance_id, compute_instance_id) to the "+
				"per-process metrics (requires --collect.compute-apps and "+
				"--collect.backend=nvml). Opt-in because it changes the label set of the "+
				"per-process series.").
			Default("false").Bool()
		collectPcieThroughput = app.Flag("collect.pcie-throughput",
			"Also export the PCIe TX/RX throughput per GPU (requires --collect.backend=nvml). "+
				"Each direction is sampled over a separate 20ms driver counter window, adding "+
				"roughly 40ms per GPU to every collection cycle (~320ms on an 8-GPU node); "+
				"pairing it with --collect.interval keeps scrapes unaffected.").
			Default("false").Bool()
		shutdownOnErr = app.Flag("shutdown-on-error",
			"Shut down the exporter if there is a fatal collection error "+
				"(a failing nvidia-smi run, or a lost GPU/driver in nvml mode). "+
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

	if err := validateWebFlags(*metricsPath, *maxRequests, *timeoutOffset); err != nil {
		return err
	}

	backendFlags := backendFlagSet{
		backend:          *collectBackend,
		nvidiaSmiCommand: *nvidiaSmiCommand,
		pcieThroughput:   *collectPcieThroughput,
		computeApps:      *collectComputeApps,
		computeAppsMIG:   *collectComputeAppsMIG,
	}

	if err := validateBackendFlags(backendFlags); err != nil {
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
		backend:          *collectBackend,
		nvidiaSmiCommand: *nvidiaSmiCommand,
		qFieldsRaw:       *qFields,
		qFieldsExclude:   *qFieldsExclude,
		interval:         *collectInterval,
		timeout:          *collectTimeout,
		computeApps:      *collectComputeApps,
		computeAppsMIG:   *collectComputeAppsMIG,
		pcieThroughput:   *collectPcieThroughput,
		onFatal:          onFatal,
	}

	registry := prometheus.NewRegistry()

	exp, err := setupExporter(ctx, eg, collectCfg, registry, logger)
	if err != nil {
		return err
	}

	mux, err := newServeMux(serveMuxConfig{
		metricsPath:   *metricsPath,
		enablePprof:   *enablePprof,
		maxRequests:   *maxRequests,
		timeoutOffset: *timeoutOffset,
	}, registry, exp, logger)
	if err != nil {
		return err
	}

	srv := &http.Server{
		ReadHeaderTimeout: *readHeaderTimeout,
		ReadTimeout:       *readTimeout,
		WriteTimeout:      *writeTimeout,
		IdleTimeout:       *idleTimeout,
		Handler:           mux,
		// request contexts descend from the process context, so shutdown also
		// cancels the collections running inside in-flight scrapes instead of
		// waiting out the shutdown grace period behind them
		BaseContext: func(net.Listener) context.Context { return ctx },
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

// backendFlagSet carries the flags whose combinations depend on the chosen
// backend.
type backendFlagSet struct {
	backend          string
	nvidiaSmiCommand string
	pcieThroughput   bool
	computeApps      bool
	computeAppsMIG   bool
}

// validateBackendFlags rejects flag combinations the chosen backend cannot
// honor, as errors rather than silent ignores.
func validateBackendFlags(flags backendFlagSet) error {
	if flags.backend == backendNVML && flags.nvidiaSmiCommand != nvidiasmi.DefaultCommand {
		// a custom command signals intent (ssh wrappers, sudo) the nvml
		// backend cannot honor
		return errors.New("--nvidia-smi-command cannot be combined with --collect.backend=nvml")
	}

	if flags.backend != backendNVML && flags.pcieThroughput {
		// the throughput counters only exist in the driver library
		return errors.New("--collect.pcie-throughput requires --collect.backend=nvml")
	}

	if flags.computeAppsMIG && flags.backend != backendNVML {
		// the per-process query output has no MIG attribution to parse
		return errors.New("--collect.compute-apps-mig requires --collect.backend=nvml")
	}

	if flags.computeAppsMIG && !flags.computeApps {
		return errors.New("--collect.compute-apps-mig requires --collect.compute-apps")
	}

	return nil
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

// validateWebFlags rejects the web flag values kingpin's types cannot.
func validateWebFlags(metricsPath string, maxRequests int, timeoutOffset time.Duration) error {
	if err := validateMetricsPath(metricsPath); err != nil {
		return err
	}

	if maxRequests < 0 {
		return fmt.Errorf("web.max-requests must not be negative, got %d", maxRequests)
	}

	if timeoutOffset < 0 {
		return fmt.Errorf("web.timeout-offset must not be negative, got %s", timeoutOffset)
	}

	return nil
}

// reservedPaths are the routes the exporter owns, which the telemetry path
// must not collide with. A trailing slash reserves the whole subtree, no
// trailing slash reserves exactly that path. The pprof subtree is reserved
// even in runs that do not enable pprof, so enabling it later cannot turn a
// working configuration into a startup failure.
var reservedPaths = []string{"/-/healthy", "/-/ready", "/debug/pprof/"}

// validateMetricsPath rejects telemetry path values that would collide with
// the exporter's own routes or make the route registration panic at startup.
func validateMetricsPath(metricsPath string) error {
	if err := validateMetricsPathShape(metricsPath); err != nil {
		return err
	}

	for _, reserved := range reservedPaths {
		subtree := strings.HasSuffix(reserved, "/")
		if metricsPath == strings.TrimSuffix(reserved, "/") || (subtree && strings.HasPrefix(metricsPath, reserved)) {
			return fmt.Errorf("web.telemetry-path %q collides with the exporter's own routes", metricsPath)
		}
	}

	return nil
}

// validateMetricsPathShape rejects malformed telemetry path values. Escapes
// and mux pattern syntax are rejected wholesale rather than interpreted: the
// mux unescapes and parses patterns, so a value like "/-/%68ealthy" or
// "/x{y}" is either a disguised collision or a route that can never be
// matched the way it reads.
func validateMetricsPathShape(metricsPath string) error {
	switch {
	case !strings.HasPrefix(metricsPath, "/"):
		return fmt.Errorf("web.telemetry-path must start with a slash, got %q", metricsPath)
	case metricsPath == "/":
		return errors.New(`web.telemetry-path must not be "/", it would collide with the landing page`)
	case strings.HasSuffix(metricsPath, "/"):
		return fmt.Errorf("web.telemetry-path must not end with a slash, got %q", metricsPath)
	case path.Clean(metricsPath) != metricsPath:
		return fmt.Errorf("web.telemetry-path must be a clean path without empty or relative segments, got %q",
			metricsPath)
	case strings.ContainsAny(metricsPath, "{}%?#"):
		return fmt.Errorf("web.telemetry-path must not contain the characters {}%%?#, got %q", metricsPath)
	case strings.ContainsFunc(metricsPath, unicode.IsSpace):
		return fmt.Errorf("web.telemetry-path must not contain whitespace, got %q", metricsPath)
	default:
		return nil
	}
}

// Collection backend names, the values of --collect.backend.
const (
	backendExec = "exec"
	backendNVML = "nvml"
)

// DefaultBackend is the default value of --collect.backend. The regular
// builds default to exec; the nvml release flavor overrides this to nvml at
// build time, so the artifact whose whole point is the nvml backend uses it
// out of the box (the flag still switches either build both ways).
var DefaultBackend = backendExec

// collectConfig carries the collection-related settings from the flags to the
// exporter setup.
type collectConfig struct {
	backend          string
	nvidiaSmiCommand string
	qFieldsRaw       string
	qFieldsExclude   string
	interval         time.Duration
	timeout          time.Duration
	computeApps      bool
	computeAppsMIG   bool
	pcieThroughput   bool
	onFatal          func(error)
}

// setupExporter resolves the query fields, builds the collection source
// (adding the background collector to the errgroup when an interval is set),
// and builds the exporter. The exporter itself is returned instead of
// registered: the metrics handler collects it under each scrape's own
// context, so it lives in a per-scrape registry there. The given registry
// gets the collectors whose output is scrape-independent.
func setupExporter(
	ctx context.Context,
	eg *errgroup.Group,
	cfg collectConfig,
	registry *prometheus.Registry,
	logger *slog.Logger,
) (*exporter.GPUExporter, error) {
	resolved, query, exitCodeMetric, err := setupBackend(ctx, eg, cfg, logger)
	if err != nil {
		return nil, err
	}

	var src collect.Source

	switch {
	case cfg.interval > 0:
		cached := collect.NewCached(query, cfg.interval, cfg.timeout, cfg.onFatal, logger)

		eg.Go(func() error { return cached.Run(ctx) })

		src = cached
	default:
		src = collect.NewLive(query, cfg.timeout, cfg.onFatal, logger)
	}

	features := exporter.Features{
		ComputeApps:         cfg.computeApps,
		ComputeAppMIGLabels: cfg.computeAppsMIG,
		// the extras families exist only in the nvml backend
		PCIeThroughput: cfg.pcieThroughput,
		Energy:         cfg.backend == backendNVML,
		MIG:            cfg.backend == backendNVML,
	}

	exp := exporter.New(ctx, exporter.DefaultPrefix, resolved, src, features, exitCodeMetric, logger)

	// the go and process collectors keep the exposed families identical to
	// what the default registry used to serve
	for _, collector := range []prometheus.Collector{
		clientversion.NewCollector(appName),
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
	} {
		if err = registry.Register(collector); err != nil {
			return nil, fmt.Errorf("failed to register collector: %w", err)
		}
	}

	return exp, nil
}

// setupBackend resolves the query fields and builds the collection function
// for the configured backend. The exec backend resolves fields by asking
// nvidia-smi; the nvml backend resolves against its compiled catalog and
// reports collection status as an NVML return code under its own metric
// name.
func setupBackend(
	ctx context.Context,
	eg *errgroup.Group,
	cfg collectConfig,
	logger *slog.Logger,
) (nvidiasmi.ResolvedFields, collect.QueryFunc, exporter.ExitCodeMetric, error) {
	if cfg.backend == backendNVML {
		backend, err := nvmlnative.New(logger)
		if err != nil {
			return nvidiasmi.ResolvedFields{}, nil, exporter.ExitCodeMetric{},
				fmt.Errorf("failed to set up the nvml backend: %w", err)
		}

		resolved, err := nvmlnative.Resolve(
			cfg.qFieldsRaw, cfg.qFieldsExclude, backend.DriverVersion(), logger)
		if err != nil {
			backend.Close()

			return nvidiasmi.ResolvedFields{}, nil, exporter.ExitCodeMetric{},
				fmt.Errorf("failed to resolve query fields: %w", err)
		}

		// tie NVML shutdown to the application lifetime, best-effort: a
		// collection stuck inside the driver makes Close skip the shutdown
		// call rather than delay process exit
		eg.Go(func() error {
			<-ctx.Done()

			backend.Close()

			return nil
		})

		opts := nvmlnative.CollectOptions{
			ComputeApps:    cfg.computeApps,
			PCIeThroughput: cfg.pcieThroughput,
			Energy:         true,
			MIG:            true,
		}

		return resolved, backend.QueryFunc(resolved, opts), exporter.NVMLReturnCodeMetric, nil
	}

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
		return nvidiasmi.ResolvedFields{}, nil, exporter.ExitCodeMetric{},
			fmt.Errorf("failed to resolve query fields: %w", err)
	}

	// the CUDA version is not a query field and is effectively constant for
	// the process lifetime (it changes with the driver, which requires the
	// GPUs to be idle), so it is read once at startup, never per scrape
	cudaVersion := nvidiasmi.QueryCudaVersion(
		ctx, cfg.nvidiaSmiCommand, cfg.timeout, nvidiasmi.DefaultRunFunc, logger)

	return resolved, buildQueryFunc(cfg, resolved, cudaVersion, logger), exporter.ExecExitCodeMetric, nil
}

// buildQueryFunc builds the collection cycle: the GPU query, plus the
// per-process query when enabled. The returned error and exit code describe
// the GPU query alone; the per-process query fails softly inside the Reading,
// per the contract on collect.QueryFunc.
func buildQueryFunc(
	cfg collectConfig,
	resolved nvidiasmi.ResolvedFields,
	cudaVersion string,
	logger *slog.Logger,
) collect.QueryFunc {
	return func(queryCtx context.Context) (collect.Reading, int, error) {
		table, exitCode, err := nvidiasmi.Query(
			queryCtx, cfg.nvidiaSmiCommand, resolved.Query, nvidiasmi.DefaultRunFunc)
		if err != nil {
			return collect.Reading{}, exitCode, fmt.Errorf("failed to query gpus: %w", err)
		}

		reading := collect.Reading{Table: table}
		reading.Extras.CUDAVersion = cudaVersion

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

// serveMuxConfig carries the web flags into the mux construction.
type serveMuxConfig struct {
	metricsPath   string
	enablePprof   bool
	maxRequests   int
	timeoutOffset time.Duration
}

// newServeMux builds the HTTP mux: the landing page on exactly the root path
// (anything unknown is a 404), the metrics endpoint, the health endpoints and
// optionally pprof.
func newServeMux(
	cfg serveMuxConfig,
	registry *prometheus.Registry,
	exp *exporter.GPUExporter,
	logger *slog.Logger,
) (*http.ServeMux, error) {
	mux := http.NewServeMux()

	landingPage, err := web.NewLandingPage(web.LandingConfig{
		Name:        "Nvidia GPU Exporter",
		Description: "Prometheus exporter for Nvidia GPUs, using nvidia-smi.",
		Version:     version.Info(),
		Links: []web.LandingLinks{
			{Address: cfg.metricsPath, Text: "Metrics"},
		},
		Profiling: strconv.FormatBool(cfg.enablePprof),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create the landing page: %w", err)
	}

	mux.Handle("GET /{$}", landingPage)
	mux.Handle("GET "+cfg.metricsPath, newMetricsHandler(cfg, registry, exp, logger))

	// process-level health checks: reachable means healthy. Deliberately
	// independent of collection success, a host whose nvidia-smi is failing
	// must stay scrapeable so the health metrics can report the failure.
	mux.HandleFunc("GET /-/healthy", healthHandler("Healthy"))
	mux.HandleFunc("GET /-/ready", healthHandler("Ready"))

	if cfg.enablePprof {
		logger.Info("pprof endpoints enabled")
		registerPprof(mux)
	}

	return mux, nil
}

// newMetricsHandler builds the metrics endpoint. Each scrape gathers the
// exporter under the scrape's own context through a per-scrape registry,
// since the collector interface has no context of its own; the collectors
// whose output is scrape-independent live in the shared registry, which also
// carries the promhttp instrumentation and error counter so the handler's
// own health stays visible in the output.
func newMetricsHandler(
	cfg serveMuxConfig,
	registry *prometheus.Registry,
	exp *exporter.GPUExporter,
	logger *slog.Logger,
) http.Handler {
	opts := promhttp.HandlerOpts{
		ErrorLog:      promhttpLogger{logger: logger},
		ErrorHandling: promhttp.HTTPErrorOnError,
		Registry:      registry,
	}

	// the scrape context is derived from the request context, the linter just
	// cannot see it through the helper
	//nolint:contextcheck
	handler := http.HandlerFunc(func(writer http.ResponseWriter, req *http.Request) {
		ctx, cancel := scrapeContext(req, cfg.timeoutOffset)
		defer cancel()

		scrapeRegistry := prometheus.NewRegistry()
		if err := scrapeRegistry.Register(exp.WithContext(ctx)); err != nil {
			logger.Error("failed to register the exporter for a scrape", "err", err)
			http.Error(writer, "failed to register the exporter", http.StatusInternalServerError)

			return
		}

		// the scrape deadline travels through the context-scoped collector;
		// stamping it onto the request as well is defensive (promhttp does
		// not currently read the request context)
		promhttp.HandlerFor(prometheus.Gatherers{registry, scrapeRegistry}, opts).
			ServeHTTP(writer, req.WithContext(ctx))
	})

	return promhttp.InstrumentMetricHandler(registry, limitConcurrency(handler, cfg.maxRequests, logger))
}

// scrapeContext bounds a scrape by the timeout Prometheus advertises for it,
// minus the configured offset, on top of the request's own lifetime (which
// already ends on client disconnect). A missing, malformed or too-small
// advertised value adds no deadline, leaving the collection timeout as the
// only bound, so a bad header can never make things stricter than no header.
func scrapeContext(req *http.Request, offset time.Duration) (context.Context, context.CancelFunc) {
	raw := req.Header.Get(scrapeTimeoutHeader)
	if raw == "" {
		return req.Context(), func() {}
	}

	seconds, err := strconv.ParseFloat(raw, 64)
	if err != nil || !(seconds > 0) || seconds > maxScrapeTimeoutSeconds {
		return req.Context(), func() {}
	}

	timeout := time.Duration(seconds*float64(time.Second)) - offset
	if timeout <= 0 {
		return req.Context(), func() {}
	}

	return context.WithTimeout(req.Context(), timeout)
}

// limitConcurrency bounds the number of scrapes served at once, so overload
// (scrapes piling up behind a slow or wedged collection) turns into immediate
// 503s instead of an unbounded queue of goroutines and connections.
func limitConcurrency(next http.Handler, limit int, logger *slog.Logger) http.Handler {
	if limit <= 0 {
		return next
	}

	slots := make(chan struct{}, limit)

	return http.HandlerFunc(func(writer http.ResponseWriter, req *http.Request) {
		select {
		case slots <- struct{}{}:
			defer func() { <-slots }()

			next.ServeHTTP(writer, req)
		default:
			logger.Warn("refused a scrape: concurrent request limit reached", "limit", limit)
			http.Error(writer, fmt.Sprintf("limit of %d concurrent requests reached, try again later", limit),
				http.StatusServiceUnavailable)
		}
	})
}

// healthHandler answers a health endpoint. The check is process-level: being
// served at all is what it reports.
func healthHandler(status string) http.HandlerFunc {
	body := []byte("Nvidia GPU Exporter is " + status + ".\n")

	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")

		_, _ = w.Write(body)
	}
}

// promhttpLogger adapts the exporter's logger to the promhttp error log
// interface, so errors gathering or encoding the metrics land in the
// exporter's own logs instead of vanishing.
type promhttpLogger struct {
	logger *slog.Logger
}

func (l promhttpLogger) Println(v ...any) {
	l.logger.Error(strings.TrimSuffix(fmt.Sprintln(v...), "\n"))
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
