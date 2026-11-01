# Configuration

The exporter is configured with command line flags only. This page lists them and explains the ones that need more than their help text.

- [Command line reference](#command-line-reference)
- [TLS, authentication and FIPS 140-3](#tls-authentication-and-fips-140-3)
- [Native NVML backend](#native-nvml-backend)
- [Per-process GPU metrics](#per-process-gpu-metrics)
- [Background collection](#background-collection)
- [Collection timeout](#collection-timeout)
- [Scrape concurrency](#scrape-concurrency)
- [Health endpoints](#health-endpoints)
- [Excluding query fields](#excluding-query-fields)
- [Custom nvidia-smi command](#custom-nvidia-smi-command)
- [Remote scraping](#remote-scraping)
- [Demo mode](#demo-mode)

## Command line reference

This is the output of `nvidia_gpu_exporter --help`:

```text
usage: nvidia_gpu_exporter [<flags>]


Flags:
  -h, --[no-]help                Show context-sensitive help (also try
                                 --help-long and --help-man).
      --[no-]web.systemd-socket  Use systemd socket activation listeners instead
                                 of port listeners (Linux only).
      --web.listen-address=:9835 ...  
                                 Addresses on which to expose metrics and web
                                 interface. Repeatable for multiple addresses.
                                 Examples: `:9100` or `[::1]:9100` for http,
                                 `vsock://:9100` for vsock
      --web.config.file=""       Path to configuration file that can
                                 enable TLS or authentication. See:
                                 https://github.com/prometheus/exporter-toolkit/blob/master/docs/web-configuration.md
      --web.network="tcp"        Network type. Valid values are tcp4, tcp6 or
                                 tcp (for listening on both stacks).
      --web.read-timeout=10s     Maximum duration before timing out read of the
                                 request.
      --web.read-header-timeout=10s  
                                 Maximum duration before timing out read of the
                                 request headers.
      --web.write-timeout=15s    Maximum duration before timing out write of the
                                 response.
      --web.idle-timeout=60s     Maximum amount of time to wait for the next
                                 request when keep-alive is enabled.
      --web.telemetry-path="/metrics"  
                                 Path under which to expose metrics.
      --web.max-requests=40      Maximum number of concurrent scrapes of the
                                 metrics endpoint. Requests beyond the limit
                                 are answered with a 503 immediately instead
                                 of queueing up behind a slow collection.
                                 0 disables the limit.
      --web.timeout-offset=500ms  
                                 Offset subtracted from the scrape
                                 timeout Prometheus advertises in the
                                 X-Prometheus-Scrape-Timeout-Seconds header,
                                 leaving time for the response to reach
                                 Prometheus. The advertised timeout minus
                                 this offset bounds each scrape's collection,
                                 on top of --collect.timeout.
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
                                 Comma-separated list of query fields to
                                 exclude from being queried. Names match
                                 literally, with `*` as a wildcard for
                                 any sequence of characters (for example
                                 `remapped_rows.histogram.*`). Useful to drop
                                 fields that are slow or unsupported on a given
                                 setup.
      --collect.backend=exec     How to collect GPU metrics. `nvml`
                                 (recommended) reads the driver library
                                 (libnvidia-ml on Linux, nvml.dll on Windows)
                                 directly without nvidia-smi, exposing the core
                                 metrics plus NVML-only extras (see the docs).
                                 On Windows it is included in the standard
                                 build. On Linux it ships in the `-nvml` release
                                 artifact, where it is the default. `exec` runs
                                 nvidia-smi (the default in the standard build).
                                 `demo` serves synthetic data mimicking the
                                 nvml surface, with no GPU or driver needed,
                                 on any platform.
      --collect.interval=0       Interval at which the collection runs in the
                                 background, with scrapes serving the most
                                 recent result. When 0, the collection runs
                                 synchronously on each scrape instead.
      --collect.timeout=10s      Maximum duration a single collection cycle may
                                 take, including all the work within it (e.g.
                                 the nvidia-smi runs) and the runs at startup.
                                 0 disables the bound.
      --[no-]collect.compute-apps  
                                 Also export per-process GPU metrics
                                 (from `nvidia-smi --query-compute-apps`,
                                 or the equivalent NVML calls in nvml mode).
                                 When the exporter runs in a container, seeing
                                 other workloads' processes requires sharing
                                 the host PID namespace (hostPID in Kubernetes,
                                 --pid=host in Docker).
      --[no-]collect.graphics-apps  
                                 Also export per-process metrics for
                                 graphics contexts on a GPU (requires
                                 --collect.backend=nvml or demo). When the
                                 exporter runs in a container, seeing other
                                 workloads' processes requires sharing the
                                 host PID namespace (hostPID in Kubernetes,
                                 --pid=host in Docker).
      --demo-config=""           Path to the demo backend's config file (the
                                 fake-nvidia-smi YAML plus an extras block, see
                                 the docs). Only with --collect.backend=demo,
                                 the built-in demo setup is used when unset.
      --[no-]collect.compute-apps-mig  
                                 Add MIG attribution labels (gpu_instance_id,
                                 compute_instance_id) to the per-process
                                 metrics (requires --collect.compute-apps or
                                 --collect.graphics-apps, and the nvml or demo
                                 backend). Opt-in because it changes the label
                                 set of the per-process series.
      --[no-]collect.compute-apps-identity  
                                 Add workload identity labels (container_id,
                                 systemd_unit) from /proc/<pid>/cgroup
                                 to the per-process metrics
                                 (requires --collect.compute-apps or
                                 --collect.graphics-apps, and Linux or the demo
                                 backend). Opt-in because it changes the label
                                 set of the per-process series.
      --[no-]collect.pcie-throughput  
                                 Also export the PCIe TX/RX throughput per
                                 GPU (requires --collect.backend=nvml,
                                 the demo backend serves the family regardless).
                                 Each direction is sampled over a separate 20ms
                                 driver counter window, adding roughly 40ms per
                                 GPU to every collection cycle (~320ms on an
                                 8-GPU node). Pairing it with --collect.interval
                                 keeps scrapes unaffected.
      --[no-]shutdown-on-error   Shut down the exporter if there is a fatal
                                 collection error (a failing nvidia-smi run,
                                 or a lost GPU/driver in nvml mode). When false,
                                 the exporter logs the error and exports it as a
                                 metric without crashing.
      --[no-]web.enable-pprof    Enable pprof endpoints for profiling under
                                 /debug/pprof/. Only enable this on a trusted
                                 network, as it exposes runtime internals.
      --log.level=info           Only log messages with the given severity or
                                 above. One of: [debug, info, warn, error]
      --log.format=logfmt        Output format of log messages. One of: [logfmt,
                                 json]
      --[no-]version             Show application version.
```

## TLS, authentication and FIPS 140-3

`--web.config.file` enables HTTPS, client certificate authentication (mutual TLS) and HTTP basic authentication on the metrics and health endpoints. The file format is the [Prometheus exporter-toolkit web configuration](https://github.com/prometheus/exporter-toolkit/blob/master/docs/web-configuration.md).

Release binaries run in Go's FIPS 140-3 mode by default. They are built with `GOFIPS140=v1.0.0`, which links the Go Cryptographic Module v1.0.0 ([CMVP certificate #5247](https://csrc.nist.gov/projects/cryptographic-module-validation-program/certificate/5247)) and sets `DefaultGODEBUG=fips140=on`. With TLS enabled, the server accepts only [Go's FIPS-approved algorithms](https://go.dev/doc/security/fips140):

- TLS 1.2 and 1.3. TLS 1.2 also requires Extended Master Secret.
- Key exchange with ECDHE on `P-256`, `P-384` or `P-521`, or ML-KEM hybrids such as `X25519MLKEM768`. Plain `X25519` is refused.
- `AES-GCM` ciphers. `ChaCha20-Poly1305` is refused.
- RSA certificate keys of at least 2048 bits.

If a client needs one of the refused options, set `GODEBUG=fips140=off` in the exporter's environment.

Basic auth passwords are hashed with `bcrypt`, which is outside the FIPS module. Where strict FIPS compliance is required, authenticate scrapers with client certificates (`client_auth_type: RequireAndVerifyClientCert`) instead.

## Native NVML backend

`--collect.backend=nvml` is the recommended backend on Linux and Windows. It reads GPU metrics directly from the driver library (`libnvidia-ml.so.1` on Linux, `nvml.dll` on Windows) without spawning `nvidia-smi`.

For every query field both `nvml` and `exec` serve, the exported series is identical in name, labels and value. On top of that shared core, `nvml` exports families that only the driver library provides (documented in [METRICS.md](METRICS.md)).

The `nvidia_smi_*` metric prefix is used by both backends. It refers to the metric schema, not the collection mechanism.

```bash
nvidia_gpu_exporter --collect.backend nvml
```

On Linux, the `-nvml` release archives and image tags (e.g., `utkuozdemir/nvidia_gpu_exporter:latest-nvml`) carry this backend and default to it, so no flag is needed. Passing `--collect.backend=exec` switches them back to running `nvidia-smi`.

On Windows, the standard `nvidia_gpu_exporter.exe` binary includes both backends and loads `nvml.dll` from `System32` when `--collect.backend=nvml` is passed.

A note for semver-based image automation (e.g., a Flux `ImagePolicy`): the `-nvml` suffix parses as a semver pre-release, so filter the flavored tags explicitly:
`filterTags: {pattern: '^(?P<version>\d+\.\d+\.\d+)-nvml$', extract: '$version'}`.

What each backend supports (`demo` serves synthetic data, see [Demo mode](#demo-mode)):

| Capability | nvml (recommended) | exec (1.x default) | demo |
| --- | --- | --- | --- |
| Query-field metrics (identical names, labels, values) | yes | yes | synthetic |
| `gpu_info` with the `cuda_version` label | yes | yes | yes |
| Per-process metrics (`--collect.compute-apps`) | yes | yes | yes |
| Per-process engine utilization (`*_app_*_utilization_ratio`) | yes | no | yes |
| Graphics per-process metrics (`--collect.graphics-apps`) | yes | no | yes |
| Total energy counter (`energy_joules_total`) | yes | no | yes |
| PCIe throughput (`--collect.pcie-throughput`) | yes | no | always on |
| PCIe replay counters (`pcie_replays_total`, `pcie_replay_rollovers_total`) | yes | no | yes |
| Aggregate NVLink counters (`nvlink_tx_bytes_total`, `nvlink_rx_bytes_total`) | yes | no | yes |
| Whole-GPU GPM activity ratios (`*_activity_ratio`, `sm_occupancy_ratio`, `dram_bandwidth_utilization_ratio`) | yes | no | yes |
| Per-MIG-instance metrics (`mig_info`, `mig_memory_*`, `mig_*_ratio`) | yes | no | yes |
| Per-process MIG attribution (`--collect.compute-apps-mig`) | yes | no | yes |
| Per-process workload identity (`--collect.compute-apps-identity`) | Linux | Linux | yes |
| XID error counters and watcher status (`xid_errors_total`, `xid_watcher_enabled`) | yes | no | yes |
| Collection status metric | `nvml_return_code` | `command_exit_code` | `nvml_return_code` |
| Custom command, remote scraping (`--nvidia-smi-command`, ssh, sudo) | no | yes | no |
| Subprocess isolation against a stuck driver call | no | yes | n/a |
| Brand-new driver fields before the catalog catches up (`AUTO`) | no | yes | no |

Why use `nvml`:

- It needs no `nvidia-smi` binary, and spawns no subprocess per collection cycle.
- It exports the driver-only metric families above.
- Collection status is reported as `nvidia_smi_nvml_return_code` (`0` on success, `-1` when timed out, rejected behind a stuck collection, or when zero GPUs are visible).

When to use `exec` instead:

- You want the zero-cgo static Linux binary, or you run on a non-glibc Linux distribution, macOS, or an older Windows driver that keeps `nvml.dll` outside `System32`.
- You use `--nvidia-smi-command` for remote scraping over `ssh` or a `sudo` wrapper.
- You want subprocess isolation so a stuck driver call can be killed by `--collect.timeout`, whereas an in-process NVML call can remain blocked in a background goroutine after the scrape times out.
- You need a brand-new field added by a recent driver that is not yet in the compiled NVML catalog.

Other notes on `nvml`:

- `--collect.pcie-throughput` is opt-in because the driver samples TX and RX over two consecutive 20ms counter windows, adding roughly 40ms per GPU to each collection cycle (about 320ms on an 8-GPU node). Pair it with `--collect.interval` so scrapes do not wait on that sampling window.
- On Windows under the WDDM driver model, NVML event registration returns `ERROR_NOT_SUPPORTED`. The exporter sets `nvidia_smi_xid_watcher_enabled` to `0`, logs a warning at startup, and serves all other families normally.
- The `nvml` backend switches from `clocks_throttle_reasons_*` to `clocks_event_reasons_*` at driver 535 to match `nvidia-smi`.
- Passing `--collect.backend=nvml` to a static Linux or macOS binary, or combining a backend with a flag or query field it does not support (such as `--nvidia-smi-command` on `nvml`), fails at startup.

## Per-process GPU metrics

`--collect.compute-apps` exports one set of metrics per process holding a compute context on a GPU:

```text
nvidia_smi_compute_app_info{uuid="...",pid="1234",process_name="/usr/bin/python3"} 1
nvidia_smi_compute_app_used_memory_bytes{uuid="...",pid="1234",process_name="/usr/bin/python3"} 2.690646016e+09
nvidia_smi_compute_apps{uuid="..."} 3
nvidia_smi_compute_apps_last_collect_success 1
```

`nvidia_smi_compute_apps` reports `0` for an idle GPU. When the per-process query fails, all per-process series are omitted and `nvidia_smi_compute_apps_last_collect_success` reads `0`, so a failed query never looks like an idle GPU.

On the `nvml` and `demo` backends, `--collect.graphics-apps` exports the matching `nvidia_smi_graphics_app_*` and `nvidia_smi_graphics_apps*` families for processes holding a graphics context (Xorg, Wayland compositors, games, 3D rendering). Because `nvidia-smi` has no CSV query flag for graphics processes, `--collect.graphics-apps` is rejected when `--collect.backend=exec`.

Operational details:

- **Container PID visibility**: Process visibility follows the PID namespace. Run the container with `--pid=host` (Docker) or `hostPID: true` (Kubernetes) to see processes from other containers and pods. A host that mounts `/proc` with `hidepid` hides other users' process names from the exporter's non-root uid.
- **Windows WDDM**: The driver does not report per-process memory in WDDM mode, so `used_memory_bytes` is omitted while `compute_app_info` and `compute_apps` still work.
- **MIG attribution (`--collect.compute-apps-mig`)**: By default, processes are attributed to the parent GPU's UUID. On `nvml` and `demo`, `--collect.compute-apps-mig` adds `gpu_instance_id` and `compute_instance_id` labels to the per-process series. Per-process utilization (`*_app_*_utilization_ratio`) is omitted on MIG-enabled GPUs because the driver returns `NVML_ERROR_NOT_SUPPORTED`.
- **Workload identity (`--collect.compute-apps-identity`)**: On Linux (and `demo`), adds `container_id` and `systemd_unit` labels read from `/proc/<pid>/cgroup` so per-process series can be joined with `kube_pod_container_info` or grouped by systemd service. Requires `--collect.compute-apps` or `--collect.graphics-apps` and host PID visibility in containers, and cannot be combined with a custom `--nvidia-smi-command`. See [METRICS.md](METRICS.md#per-process-metrics-opt-in).
- **MIG container permissions**: A containerized exporter on a MIG-enabled GPU needs privileged mode, `NVIDIA_MIG_MONITOR_DEVICES=all` and host PID sharing for full visibility. Without privileged mode, per-MIG inventory, memory and activity still work, while GPU-level memory fields and per-process lists read `[Insufficient Permissions]`. See the [chart README](../charts/nvidia-gpu-exporter/README.md#per-process-gpu-metrics) for the Kubernetes `securityContext` block.
- **Series cardinality**: Because the `pid` label changes as processes start and stop, short-lived processes create short-lived time series. This is why per-process collection is opt-in.

## Background collection

By default (`--collect.interval=0`), the exporter collects once per scrape, and concurrent scrapes share the in-flight collection.

Setting `--collect.interval` runs collection on a background timer instead, and scrapes serve the latest snapshot immediately without waiting:

```bash
nvidia_gpu_exporter --collect.interval 15s
```

Keep in mind:

- A served reading can be up to one interval plus the collection duration old. Use `nvidia_smi_last_collect_success_timestamp_seconds` to check snapshot age.
- A staleness alert should allow a few intervals plus `--collect.timeout` (for example `3 * 15s + 10s = 55s`) so a single failed cycle does not page:

  ```text
  time() - nvidia_smi_last_collect_success_timestamp_seconds > 55
    or nvidia_smi_last_collect_success == 0
  ```

- A scrape arriving before the first background collection finishes is answered immediately with `nvidia_smi_last_collect_success 0` and no GPU series.
- When a background run fails, GPU series are omitted until the next successful run rather than serving stale readings.

## Collection timeout

Every collection cycle, including startup discovery, is bounded by `--collect.timeout` (default `10s`, `0` disables the bound). A cycle that exceeds the timeout is recorded as a failed collection (`nvidia_smi_last_collect_success 0`) instead of hanging scrapes or startup. On the `exec` backend, a timed-out subprocess is killed on a best-effort basis, and with a wrapper command in `--nvidia-smi-command` (such as `ssh`), only the local wrapper process itself is signalled.

In synchronous mode (`--collect.interval=0`), a scrape's collection is also bounded by the scrape timeout Prometheus advertises in `X-Prometheus-Scrape-Timeout-Seconds` (minus `--web.timeout-offset`) and by the HTTP connection lifetime. If you raise `--collect.timeout`, keep it below `--web.write-timeout`.

## Scrape concurrency

The metrics endpoint serves at most `--web.max-requests` concurrent scrapes (default `40`, `0` disables the limit). Requests beyond the limit receive `503 Service Unavailable` immediately so scrapes cannot pile up behind a slow collection.

## Health endpoints

`/-/healthy` and `/-/ready` answer `200 OK` as long as the HTTP server is up. They do not depend on collection success, so a node with a failing GPU or driver stays scrapeable and its failure metrics remain visible to Prometheus. The Helm chart's liveness and readiness probes use these endpoints.

`/-/gpu-healthy` inspects the latest cached collection snapshot without triggering a new collection. It answers `200 OK` when all observed GPUs are healthy, or `503 Service Unavailable` with one plain-text line per failing condition when:

- no collection has completed yet, or the most recent collection failed
- `nvidia_smi_gpu_recovery_action > 0` on any GPU
- any GPU UUID seen in an earlier successful collection is missing from the latest snapshot (such as a GPU that fell off the bus)
- a reset/reboot-class XID (`48`, `62`, `64`, `74`, `79`, `95`, `119` or `120`) has been observed since startup (`nvml` and `demo` backends)

In synchronous mode (`--collect.interval=0`), collections only run when `/metrics` is scraped, so `/-/gpu-healthy` answers `503` until the first scrape. Pair it with `--collect.interval` when polling `/-/gpu-healthy` independently of Prometheus scrapes.

Use `/-/gpu-healthy` for external node health checks (such as Kubernetes Node Problem Detector), and never set it as the exporter container's own liveness or readiness probe.

## Excluding query fields

By default (`--query-field-names=AUTO`), the exporter queries every available field. Use `--query-field-names-exclude` to skip specific fields (literal match, with `*` as a wildcard):

```bash
# Skip remapped_rows.histogram.* fields, which trigger kernel warnings in some vGPU guests
nvidia_gpu_exporter --query-field-names-exclude "remapped_rows.histogram.*"

# Exclude multiple specific fields
nvidia_gpu_exporter --query-field-names-exclude "inforom.checksum_validation,fan.speed"
```

Identity fields backing `nvidia_smi_gpu_info` (`uuid`, `name`) cannot be excluded.

## Custom nvidia-smi command

With `--collect.backend=exec`, `--nvidia-smi-command` sets the executable path and optional arguments (for example `sudo nvidia-smi` or an `ssh` wrapper). If a path contains spaces, quote it so the quotes reach the exporter as part of the flag value:

```bash
# Windows, nvidia-smi not on PATH
nvidia_gpu_exporter --nvidia-smi-command '"C:\Program Files\NVIDIA Corporation\NVSMI\nvidia-smi.exe"'

# Wrapper plus a quoted path
nvidia_gpu_exporter --nvidia-smi-command 'sudo "/opt/my tools/nvidia-smi"'
```

Where no shell strips outer quotes (a Windows service or a systemd unit), a single layer of quotes is enough.

## Remote scraping

With `--collect.backend=exec`, the exporter can scrape a remote GPU host over SSH:

```bash
nvidia_gpu_exporter --nvidia-smi-command "ssh -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null SSH_USER@SSH_HOST nvidia-smi"
```

This requires a binary install or a custom container image that includes `ssh`, since the distroless exporter image contains only the exporter binary (see [INSTALL.md](INSTALL.md#no-shell-in-the-image)).

## Demo mode

`--collect.backend=demo` serves synthetic data matching the `nvml` backend's metric surface, with no GPU, driver or Linux required:

```bash
nvidia_gpu_exporter --collect.backend demo
```

The built-in setup simulates two H200 GPUs, a MIG topology on the first GPU, and a pre-seeded XID history. Pass `--demo-config` to customize the scenario:

```yaml
# Embedded captures:
# linux-x86_64__nvidia-h200__590.48.01.txt
# linux-x86_64__nvidia-geforce-rtx-4080-super__595.71.05.txt
capture: linux-x86_64__nvidia-h200__590.48.01.txt
state: load      # idle or load
gpus: 4          # replicate the captured GPU into this many
fluctuate: true  # jitter moving values between scrapes
extras:
  seed: 42
  energy-fallback-power-watts: 120
  pcie:
    tx-bytes-per-second: {min: 0.2e9, max: 3e9}
    rx-bytes-per-second: {min: 0.5e9, max: 8e9}
  mig:
    - gpu: 0         # index into the simulated GPU list
      instances:
        - {gi: 2, profile: 3g.71gb, cis: 2, busy: true}
        - {gi: 7, profile: 1g.18gb}
  xids:
    initial: [{gpu: 1, xid: 79, count: 2}]
    interval: 10m    # omit to disable periodic XID injection
    codes: [13, 31, 79]
```

The top-level keys (`capture`, `state`, `gpus`, `fluctuate`) configure the embedded fake `nvidia-smi` (see `cmd/fake-nvidia-smi` for the full key reference), and `extras` configures the synthesized NVML-only families. The config file is re-read on every collection cycle so edits apply without a restart (except the `cuda_version` label, which is read once at startup).

Differences from the real `nvml` backend worth knowing when developing dashboards:

- PCIe throughput is always on in `demo` mode, whereas on `nvml` it requires `--collect.pcie-throughput`.
- Per-process utilization is synthesized on every GPU in `demo` mode so a single card can exercise both MIG and process panels at once, whereas `nvml` omits `*_app_*_utilization_ratio` on MIG-enabled GPUs.
- For deterministic values across runs, set `extras.seed` and `fluctuate: false`.
