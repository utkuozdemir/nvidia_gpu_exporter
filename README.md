# nvidia_gpu_exporter

[![build](https://github.com/utkuozdemir/nvidia_gpu_exporter/actions/workflows/build.yml/badge.svg)](https://github.com/utkuozdemir/nvidia_gpu_exporter/actions/workflows/build.yml)
[![Coverage Status](https://coveralls.io/repos/github/utkuozdemir/nvidia_gpu_exporter/badge.svg?branch=master)](https://coveralls.io/github/utkuozdemir/nvidia_gpu_exporter?branch=master)
[![Go Report Card](https://goreportcard.com/badge/github.com/utkuozdemir/nvidia_gpu_exporter?kill_cache=1)](https://goreportcard.com/report/github.com/utkuozdemir/nvidia_gpu_exporter)
![Latest GitHub release](https://img.shields.io/github/release/utkuozdemir/nvidia_gpu_exporter.svg)
[![GitHub license](https://img.shields.io/github/license/utkuozdemir/nvidia_gpu_exporter)](https://github.com/utkuozdemir/nvidia_gpu_exporter/blob/master/LICENSE)
![GitHub all releases](https://img.shields.io/github/downloads/utkuozdemir/nvidia_gpu_exporter/total)
![Docker Pulls](https://img.shields.io/docker/pulls/utkuozdemir/nvidia_gpu_exporter)

Nvidia GPU exporter for prometheus, using `nvidia-smi` binary to gather metrics.

## Introduction

There are many Nvidia GPU exporters out there however they have problems such as not being maintained, 
not providing pre-built binaries, having a dependency to Linux and/or Docker, 
targeting enterprise setups (DCGM) and so on.

This is a simple exporter that uses `nvidia-smi(.exe)` binary to collect, parse and export metrics.
This makes it possible to run it on Windows and get GPU metrics while gaming - no Docker or Linux required.

This project is based on [a0s/nvidia-smi-exporter](https://github.com/a0s/nvidia-smi-exporter).
However, this one is written in Go to produce a single, static binary.

**If you are a gamer who's into monitoring, you are in for a treat.**

## Highlights

- Will work on any system that has `nvidia-smi(.exe)?` binary - Windows, Linux, MacOS... No C bindings required
- Doesn't even need to run the monitored machine: can be configured to execute `nvidia-smi` command remotely
- No need for a Docker or Kubernetes environment
- Auto-discovery of the metric fields `nvidia-smi` can expose (future-compatible)
- Comes with its own [Grafana dashboard](https://grafana.com/grafana/dashboards/14574)

## Visualization

You can use the official [Grafana dashboard](https://grafana.com/grafana/dashboards/14574)
to see your GPU metrics in a nicely visualized way.

Here's how it looks like:
![Grafana dashboard](https://raw.githubusercontent.com/utkuozdemir/nvidia_gpu_exporter/master/grafana/dashboard.png)


## Installation

### Installing as a Windows Service

Requirements:
- [Scoop package manager](https://scoop.sh)
- [NSSM](https://nssm.cc/download) (get the latest pre-release version)

Installation steps:
1. Open a privileged powershell prompt (right click - Run as administrator)
2. Run the following commands:

```powershell
scoop bucket add nvidia_gpu_exporter https://github.com/utkuozdemir/scoop_nvidia_gpu_exporter.git
scoop install nvidia_gpu_exporter/nvidia_gpu_exporter --global
New-NetFirewallRule -DisplayName "Nvidia GPU Exporter" -Direction Inbound -Action Allow -Protocol TCP -LocalPort 9835
nssm install nvidia_gpu_exporter nvidia_gpu_exporter "C:\ProgramData\scoop\apps\nvidia_gpu_exporter\current\nvidia_gpu_exporter.exe"
Start-Service nvidia_gpu_exporter
```

### By downloading the binaries (MacOS/Linux/Windows)

1. Go to the [releases](https://github.com/utkuozdemir/nvidia_gpu_exporter/releases) and download
   the latest release archive for your platform.
2. Extract the archive.
3. Move the binary to somewhere in your `PATH`.

Sample steps for Linux 64-bit:
```bash
$ VERSION=0.1.2
$ wget https://github.com/utkuozdemir/nvidia_gpu_exporter/releases/download/v${VERSION}/nvidia_gpu_exporter_${VERSION}_linux_x86_64.tar.gz
$ tar -xvzf nvidia_gpu_exporter_${VERSION}_linux_x86_64.tar.gz
$ mv nvidia_gpu_exporter /usr/local/bin
$ nvidia_gpu_exporter --help
```

## Running in Docker
You can run the exporter in a Docker container. For it to work, you will need to ensure the following:
- The `nvidia-smi` binary is bind-mounted from the host to the container under its `PATH`
- The devices `/dev/nvidiaX` (depends on the number of GPUs you have) and `/dev/nvidiactl` are mounted into the container
- The library files `libnvidia-ml.so` and `libnvidia-ml.so.1` are mounted inside the container.
  They are typically found under `/usr/lib/x86_64-linux-gnu/` or `/usr/lib/i386-linux-gnu/`.
  Locate them in your host to ensure you are mounting them from the correct path.

A working example with all these combined (tested in `Ubuntu 20.04`):
```bash
docker run -d \
--name nvidia_smi_exporter \
--restart unless-stopped \
--device /dev/nvidiactl:/dev/nvidiactl \
--device /dev/nvidia0:/dev/nvidia0 \
-v /usr/lib/x86_64-linux-gnu/libnvidia-ml.so:/usr/lib/x86_64-linux-gnu/libnvidia-ml.so \
-v /usr/lib/x86_64-linux-gnu/libnvidia-ml.so.1:/usr/lib/x86_64-linux-gnu/libnvidia-ml.so.1 \
-v /usr/bin/nvidia-smi:/usr/bin/nvidia-smi \
-p 9835:9835 \
utkuozdemir/nvidia_gpu_exporter:0.1.4
```

## Usage

The usage of the binary is the following:

```
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

# Contributing

See [CONTRIBUTING](CONTRIBUTING.md) for details.
