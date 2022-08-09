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
