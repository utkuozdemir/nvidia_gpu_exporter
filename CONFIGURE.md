# Configuration

You can find the configuration reference on this page.

## Command Line Reference

The exporter binary accepts the following arguments:

```text
usage: nvidia_gpu_exporter [<flags>]

Flags:
  -h, --help                Show context-sensitive help (also try --help-long and --help-man).
      --web.config.file=""  [EXPERIMENTAL] Path to configuration file that can enable TLS or authentication.
      --web.listen-address=":9835"
                            Address to listen on for web interface and telemetry.
      --web.telemetry-path="/metrics"
                            Path under which to expose metrics.
      --nvidia-smi-command="nvidia-smi"
                            Path or command to be used for the nvidia-smi executable
      --query-field-names="AUTO"
                            Comma-separated list of the query fields. You can find out possible fields by running `nvidia-smi --help-query-gpus`. The value `AUTO` will
                            automatically detect the fields to query.
      --query-field-names-exclude=""
                            Comma-separated list of query fields to exclude from being queried. Names match literally, with `*` as a wildcard for any sequence of characters (e.g. `remapped_rows.histogram.*`). Useful to drop fields that are slow or unsupported on a given setup.
      --[no-]web.enable-pprof
                            Enable pprof endpoints for profiling under /debug/pprof/. Only enable this on a trusted network, as it exposes runtime internals.
      --log.level=info      Only log messages with the given severity or above. One of: [debug, info, warn, error]
      --log.format=logfmt   Output format of log messages. One of: [logfmt, json]
      --version             Show application version.
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
