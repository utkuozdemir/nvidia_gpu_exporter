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
                                executable
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
      --collect.timeout=10s     Maximum duration a single nvidia-smi run may
                                take, including the runs at startup. 0 disables
                                the bound.
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

Every `nvidia-smi` run, including the field discovery runs at startup, is
bounded by `--collect.timeout` (default `10s`). A run that exceeds it counts
as a failed collection instead of hanging the scrape or the exporter startup.
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
