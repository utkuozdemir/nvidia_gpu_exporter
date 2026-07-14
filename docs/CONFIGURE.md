# Configuration

You can find the configuration reference on this page.

## Command Line Reference

The exporter binary accepts the following arguments:

```text
usage: nvidia_gpu_exporter [<flags>]

Flags:
  -h, --[no-]help               Show context-sensitive help (also try
                                --help-long and --help-man).
      --web.listen-address=:9835 ...
                                Addresses on which to expose metrics and web
                                interface. Repeatable for multiple addresses.
                                Examples: `:9100` or `[::1]:9100` for http,
                                `vsock://:9100` for vsock
      --web.config.file=""      Path to configuration file that can
                                enable TLS or authentication. See:
                                https://github.com/prometheus/exporter-toolkit/blob/master/docs/web-configuration.md
      --web.network="tcp"       Network type. Valid values are tcp4, tcp6 or tcp
                                (for listening on both stacks).
      --web.read-timeout=10s    Maximum duration before timing out read of the
                                request.
      --web.read-header-timeout=10s
                                Maximum duration before timing out read of the
                                request headers.
      --web.write-timeout=15s   Maximum duration before timing out write of the
                                response.
      --web.idle-timeout=60s    Maximum amount of time to wait for the next
                                request when keep-alive is enabled.
      --web.telemetry-path="/metrics"
                                Path under which to expose metrics.
      --nvidia-smi-command="nvidia-smi"
                                Path or command to be used for the nvidia-smi
                                executable. Multiple words run the first as the
                                executable with the rest as its arguments (e.g.
                                `sudo nvidia-smi` or an ssh wrapper).
                                A path containing spaces must be quoted,
                                and the quotes must be part of this value
                                itself, not consumed by the shell you set the
                                flag from: --nvidia-smi-command '"C:\Program
                                Files\...\nvidia-smi.exe"'.
      --query-field-names="AUTO"
                                Comma-separated list of the query fields.
                                You can find out possible fields by running
                                `nvidia-smi --help-query-gpu`. The value `AUTO`
                                will automatically detect the fields to query.
      --query-field-names-exclude=""
                                Comma-separated list of query fields to exclude
                                from being queried. Names match literally, with
                                `*` as a wildcard for any sequence of characters
                                (for example `remapped_rows.histogram.*`).
                                Useful to drop fields that are slow or
                                unsupported on a given setup.
      --collect.interval=0      Interval at which nvidia-smi runs in the
                                background, with scrapes serving the most recent
                                result. When 0, nvidia-smi runs synchronously on
                                each scrape instead.
      --collect.timeout=10s     Maximum duration a single collection cycle may
                                take, including all nvidia-smi runs within it
                                and the runs at startup. 0 disables the bound.
      --[no-]collect.compute-apps
                                Also export per-process GPU metrics
                                from `nvidia-smi --query-compute-apps`.
                                Adds one nvidia-smi run per collection cycle.
                                When the exporter runs in a container, seeing
                                other workloads' processes requires sharing
                                the host PID namespace (hostPID in Kubernetes,
                                --pid=host in Docker).
      --[no-]shutdown-on-error  Shut down the exporter if there is an error
                                querying nvidia-smi. When false, exporter will
                                simply log this error and export it as a metric,
                                but will not crash.
      --[no-]web.enable-pprof   Enable pprof endpoints for profiling under
                                /debug/pprof/. Only enable this on a trusted
                                network, as it exposes runtime internals.
      --log.level=info          Only log messages with the given severity or
                                above. One of: [debug, info, warn, error]
      --log.format=logfmt       Output format of log messages. One of: [logfmt,
                                json]
      --[no-]version            Show application version.
```

## Custom nvidia-smi command

`--nvidia-smi-command` accepts a full path, or multiple words where the first
is the executable and the rest are passed to it as arguments. If the path
contains spaces, quote it with single or double quotes:

```bash
# Windows, nvidia-smi not on PATH
nvidia_gpu_exporter --nvidia-smi-command '"C:\Program Files\NVIDIA Corporation\NVSMI\nvidia-smi.exe"'

# a wrapper plus a quoted path
nvidia_gpu_exporter --nvidia-smi-command 'sudo "/opt/my tools/nvidia-smi"'
```

The quotes must reach the exporter as part of the flag value itself. When you
set the flag from a shell, the shell consumes the outermost quotes, so nest
them as in the examples above — `--nvidia-smi-command "C:\Program Files\..."`
alone will NOT work, because the exporter never sees those quotes. Where no
shell is involved (a Windows service configuration, a systemd unit's exec
line), plain quotes in the value are enough.

Quote-aware parsing only kicks in when the value actually contains a quote
character, so existing unquoted commands keep working exactly as before,
including Windows paths with backslashes. No variable expansion happens.

## Remote scraping configuration

The exporter can be configured to scrape metrics from a remote machine.

An example use case is running the exporter in a **Raspberry Pi** in
your home network while scraping the metrics from your PC over SSH.

The exporter supports arbitrary commands with arguments to produce `nvidia-smi`-like output.
Therefore, configuration is pretty straightforward.

Simply override the `--nvidia-smi-command` command-line argument (replace `SSH_USER` and `SSH_HOST` with SSH credentials):

```bash
nvidia_gpu_exporter --nvidia-smi-command "ssh -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null SSH_USER@SSH_HOST nvidia-smi"
```

## Excluding query fields

By default (`--query-field-names=AUTO`) the exporter queries every field
`nvidia-smi` reports. On some setups a few fields are slow to read or trigger
warnings, and you may want to skip them while keeping everything else on `AUTO`.

Use `--query-field-names-exclude` for that. Names match literally, and `*` is a
wildcard for any sequence of characters.

```bash
# Skip the remapped_rows.histogram.* fields, which trigger kernel warnings in some vGPU guests
nvidia_gpu_exporter --query-field-names-exclude "remapped_rows.histogram.*"

# Exclude multiple specific fields
nvidia_gpu_exporter --query-field-names-exclude "inforom.checksum_validation,fan.speed"
```

Fields backing the `nvidia_smi_gpu_info` metric (such as `uuid` and `name`)
cannot be excluded, since the rest of the metrics are labeled by GPU UUID.

## Background collection

By default the exporter runs `nvidia-smi` once per scrape. If the exporter is
scraped frequently or by several Prometheus servers at once, that means a lot
of short-lived `nvidia-smi` processes.

Setting `--collect.interval` decouples the two: `nvidia-smi` runs in the
background at the given interval, and scrapes serve the most recent result.
The number of `nvidia-smi` runs then depends only on the interval, no matter
how often the exporter is scraped.

```bash
# Run nvidia-smi every 15 seconds regardless of scrape traffic
nvidia_gpu_exporter --collect.interval 15s
```

Two things to keep in mind in this mode:

- A served reading can be up to one interval old. Prometheus timestamps
  samples at scrape time, so use `nvidia_smi_last_collect_success_timestamp_seconds`
  to see how fresh the data actually is. A staleness alert should also cover
  the case where no collection has succeeded yet, for example:

  ```text
  time() - nvidia_smi_last_collect_success_timestamp_seconds > 45
    or nvidia_smi_last_collect_success == 0
  ```

- When a background run fails, the GPU metrics disappear from the output until
  the next successful run instead of going stale silently.
  `nvidia_smi_last_collect_success` reports the failure either way.

## Collection timeout

Every collection cycle, including the field discovery runs at startup, is
bounded by `--collect.timeout` (default `10s`). All `nvidia-smi` runs within
one cycle share the budget (with `--collect.compute-apps` there are two). A
cycle that exceeds it counts as a failed collection instead of hanging the
scrape or the exporter startup.
This matters on setups where `nvidia-smi` can wedge on a driver issue.
Cleaning up a killed process that refuses to die can take a couple of seconds
on top of the timeout itself.

Set `--collect.timeout 0` to restore the old unbounded behavior. The bound is
best-effort: it reliably kills a normal `nvidia-smi`, but it cannot interrupt
a process stuck in an uninterruptible kernel wait, and with a wrapper command
(such as the SSH setup above) it only signals the wrapper itself.

If you raise `--collect.timeout` in synchronous mode, keep it below
`--web.write-timeout` and below the Prometheus `scrape_timeout`, otherwise
a slow run fails at the HTTP or Prometheus layer first. Background mode is
mostly unaffected since scrapes only read the cached result, with one
exception: a scrape arriving before the very first collection completes
waits for it, so it can take up to `--collect.timeout` once at startup.

## Per-process GPU metrics

`--collect.compute-apps` additionally exports one set of metrics per process
holding a compute context on a GPU, read from `nvidia-smi
--query-compute-apps`. The main use case is a machine running several
workloads on one GPU, where you want to see which process uses what.

```text
nvidia_smi_compute_app_info{uuid="...",pid="1234",process_name="/usr/bin/python3"} 1
nvidia_smi_compute_app_used_memory_bytes{uuid="...",pid="1234",process_name="/usr/bin/python3"} 2.690646016e+09
nvidia_smi_compute_apps{uuid="..."} 3
nvidia_smi_compute_apps_last_collect_success 1
```

`nvidia_smi_compute_apps` reports an explicit `0` for an idle GPU. When the
per-process query itself fails, all per-process series disappear and
`nvidia_smi_compute_apps_last_collect_success` reads `0`, so a query failure
never looks like an idle GPU.

Things to keep in mind:

- **Containers see only their own processes.** Process visibility follows the
  PID namespace: an exporter container without host PID sharing sees no other
  workloads. Run with `--pid=host` (Docker) or `hostPID: true` (Kubernetes,
  exposed as the `hostPID` value in the Helm chart) to see everything. The
  tradeoff is that the exporter pod can then see all host process names,
  which some security policies forbid.
- **Windows in WDDM mode reports no per-process memory.** The driver does not
  manage the memory there, so `used_gpu_memory` is not available: the
  `compute_app_info` and `compute_apps` metrics still work, the
  `used_memory_bytes` metric is absent.
- **MIG limits both attribution and container access.** Processes are
  attributed to the parent GPU's UUID, not to MIG instances. A containerized
  exporter on a MIG-enabled GPU additionally needs to run privileged with the
  `NVIDIA_MIG_MONITOR_DEVICES=all` environment variable (plus host PID
  sharing), otherwise the per-process list and even some GPU-level fields
  read `[Insufficient Permissions]`.
- **The `pid` label churns.** Every new process creates new series, and they
  disappear with the process. On machines with high process turnover this
  can bloat the time series database, which is one of the reasons the
  feature is opt-in.

## Experimental: native NVML backend

`--collect.backend=nvml` reads GPU metrics directly from the driver library
(`libnvidia-ml.so.1`) instead of running `nvidia-smi`. The exported metrics
are the same: metric names, labels and values match the default backend on
the same machine, verified field-by-field against live hardware.

```bash
nvidia_gpu_exporter --collect.backend nvml
```

What it buys:

- It needs no `nvidia-smi` binary. In containers, the NVIDIA container runtime
  injecting the driver library is enough. The `-nvml` release artifacts and image tags (for example
  `utkuozdemir/nvidia_gpu_exporter:1.5.0-nvml`) carry this backend and
  default to it, so they need no flag at all; `--collect.backend=exec`
  switches them back. Note for semver-based image automation (e.g. a Flux
  `ImagePolicy`): the `-nvml` suffix parses as a semver pre-release, so
  filter the flavored tags explicitly, for example with
  `filterTags: {pattern: '^(?P<version>\d+\.\d+\.\d+)-nvml$', extract: '$version'}`.
- It spawns no process per collection, and a single collection is cheaper
  than an `nvidia-smi` run.
- Collection failures are reported as an NVML status
  (`nvidia_smi_nvml_return_code`) instead of a process exit code. `0` means
  success; `-1` means the collection produced no NVML status at all: it was
  abandoned on timeout, rejected because a previous one is still stuck, or
  found zero visible GPUs (deliberately a failed collection, so a broken
  container device mount cannot look like a healthy idle scrape).

Current limits, while the backend is experimental:

- Linux x86_64 only, glibc-based systems (the binary is built with cgo), and
  only in the dedicated `-nvml` release artifacts and image tags. The regular
  binaries stay fully static and answer this flag with an error.
- The queryable fields are a built-in catalog. A field a future driver adds
  shows up in the default backend first; explicit `--query-field-names`
  lists work the same in both backends and fail loudly on unknown fields.
- On drivers older than the 590 branch the clock-reasons metric family may be
  spelled `clocks_throttle_reasons_*` by one backend and
  `clocks_event_reasons_*` by the other: the exact driver release that renamed
  the family is not pinned down yet.
- `--nvidia-smi-command` cannot be combined with this backend, since there
  is no command to customize. Remote scraping via an ssh wrapper needs the
  default backend.
- A wedged driver call cannot be killed the way a stuck `nvidia-smi` process
  can. `--collect.timeout` still bounds how long a scrape waits, but the
  stuck call can linger in the background. The default backend remains the
  strongest isolation against misbehaving drivers.

The `nvidia_smi_*` metric prefix stays as is in both backends: it names the
data schema, not the collection mechanism.
