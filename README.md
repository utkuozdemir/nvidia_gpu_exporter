# nvidia_gpu_exporter

[![build](https://github.com/utkuozdemir/nvidia_gpu_exporter/actions/workflows/build.yml/badge.svg)](https://github.com/utkuozdemir/nvidia_gpu_exporter/actions/workflows/build.yml)
[![codecov](https://codecov.io/gh/utkuozdemir/nvidia_gpu_exporter/branch/main/graph/badge.svg?token=JEWV818FCZ)](https://codecov.io/gh/utkuozdemir/nvidia_gpu_exporter)
[![Go Report Card](https://goreportcard.com/badge/github.com/utkuozdemir/nvidia_gpu_exporter?kill_cache=1)](https://goreportcard.com/report/github.com/utkuozdemir/nvidia_gpu_exporter)
![Latest GitHub release](https://img.shields.io/github/release/utkuozdemir/nvidia_gpu_exporter.svg)
[![GitHub license](https://img.shields.io/github/license/utkuozdemir/nvidia_gpu_exporter)](https://github.com/utkuozdemir/nvidia_gpu_exporter/blob/main/LICENSE)
![GitHub all releases](https://img.shields.io/github/downloads/utkuozdemir/nvidia_gpu_exporter/total)
![Docker Pulls](https://img.shields.io/docker/pulls/utkuozdemir/nvidia_gpu_exporter)

Nvidia GPU exporter for prometheus, using `nvidia-smi` binary to gather metrics.

---

> [!WARNING]
> Heads up: this is a side project I maintain in my spare time. I might take a long time to look at issues or PRs, or not get to them at all. Sorry in advance, and thanks for understanding.

---

## Introduction

There are many Nvidia GPU exporters out there however they have problems such as not being maintained,
not providing pre-built binaries, having a dependency to Linux and/or Docker,
targeting enterprise setups (DCGM) and so on.

This is a simple exporter that uses `nvidia-smi(.exe)` binary to collect, parse and export metrics.
This makes it possible to run it on Windows and get GPU metrics while gaming - no Docker or Linux required.

It can also skip `nvidia-smi` and read the metrics straight from the NVIDIA
Management Library (NVML), the C library `nvidia-smi` itself is built on. This
mode is experimental and exports a superset of the default mode's metrics: the
same core set plus NVML-only extras like the GPU energy counter, per-MIG-instance
metrics, XID error counters and opt-in PCIe throughput; see
[CONFIGURE.md](docs/CONFIGURE.md).

This project is based on [a0s/nvidia-smi-exporter](https://github.com/a0s/nvidia-smi-exporter).
However, this one is written in Go to produce a single, static binary.

## Use cases

- Consumer and prosumer GPUs (GeForce/RTX), where the datacenter tooling
  exposes little and `nvidia-smi` is often the only uniform source of
  utilization, memory, power and temperature
- Small Kubernetes clusters, edge boxes and homelabs that want GPU metrics
  without installing the NVIDIA GPU Operator stack
- Virtualized or restricted setups (vGPU guests, locked-down containers)
  where the deeper GPU counters are not exposed but `nvidia-smi` still answers
- Mixed fleets of old and new cards that need one exporter that behaves the
  same everywhere
- Gaming rigs, for watching your GPU stats on a dashboard while you play

If you run datacenter cards on Kubernetes with the GPU Operator already
installed, [DCGM-exporter](https://github.com/NVIDIA/dcgm-exporter) is
probably the better fit; this exporter aims at the cases above.

## Highlights

- Will work on any system that has `nvidia-smi(.exe)?` binary - Windows, Linux, MacOS... No C bindings required
- Doesn't even need to run the monitored machine: can be configured to execute `nvidia-smi` command remotely
- No need for a Docker or Kubernetes environment
- Auto-discovery of the metric fields `nvidia-smi` can expose (future-compatible)
- Optional per-process GPU metrics: see which process uses how much GPU memory
- Experimental NVML mode: reads the driver library directly instead of running `nvidia-smi`, and unlocks metrics `nvidia-smi` cannot provide (Linux)
- Comes with its own [Grafana dashboard](https://grafana.com/grafana/dashboards/14574)

## Visualization

You can use the official [Grafana dashboard](https://grafana.com/grafana/dashboards/14574)
to see your GPU metrics in a nicely visualized way.

Here's how it looks like:
![Grafana dashboard](https://raw.githubusercontent.com/utkuozdemir/nvidia_gpu_exporter/main/docs/grafana/dashboard.png)

For machines with more than one GPU there is a companion
[overview dashboard](https://github.com/utkuozdemir/nvidia_gpu_exporter/blob/main/docs/grafana/dashboard-overview.json)
that compares all GPUs of a node side by side and drills down into the
single-GPU dashboard above. Import it from the JSON file, or enable
`grafanaDashboard` in the Helm chart to get both dashboards provisioned.

![Grafana overview dashboard](https://raw.githubusercontent.com/utkuozdemir/nvidia_gpu_exporter/main/docs/grafana/dashboard-overview.png)

## Installation

See [INSTALL.md](docs/INSTALL.md) for details.

## Verifying releases

Release artifacts are signed so you can check they came from this project's
release pipeline:

- The `checksums.txt` file attached to each release is signed with GPG
  (`checksums.txt.asc`), which covers every binary, archive and package.
- The container images and the Helm chart are signed keyless with
  [cosign](https://github.com/sigstore/cosign), tied to the release workflow's
  identity.

See [INSTALL.md](docs/INSTALL.md) for the exact verification commands, and the
[chart README](charts/nvidia-gpu-exporter/README.md) for the chart.

## Configuration

See [CONFIGURE.md](docs/CONFIGURE.md) for details.

## Metrics

See [METRICS.md](docs/METRICS.md) for details.

## Contributing

See [CONTRIBUTING.md](.github/CONTRIBUTING.md) for details.

### Help wanted: contribute a GPU capture

The exporter parses `nvidia-smi` output, which differs across GPU models, driver
versions and operating systems. If you have hardware that isn't covered yet
(datacenter cards, MIG, multi-GPU, Windows/WSL2, brand-new drivers...), you can
help a lot by capturing your `nvidia-smi` output with one command:

```bash
./internal/captures/collect.sh          # add --load for an under-load sample too
```

It needs only `nvidia-smi`, `bash`, and the standard core utilities (`awk`,
`sed`, ...), runs read-only, and masks identifiers (GPU UUID, serial, hostname)
by default. It writes one `.txt` file: commit it and open a PR, or attach it to
an issue. See [internal/captures/README.md](internal/captures/README.md).

## Star History

<!-- markdownlint-disable no-inline-html -->
<a href="https://star-history.com/#utkuozdemir/nvidia_gpu_exporter&Date">
 <picture>
   <source media="(prefers-color-scheme: dark)" srcset="https://api.star-history.com/svg?repos=utkuozdemir/nvidia_gpu_exporter&type=Date&theme=dark" />
   <source media="(prefers-color-scheme: light)" srcset="https://api.star-history.com/svg?repos=utkuozdemir/nvidia_gpu_exporter&type=Date" />
   <img alt="Star History Chart" src="https://api.star-history.com/svg?repos=utkuozdemir/nvidia_gpu_exporter&type=Date" />
 </picture>
</a>
<!-- markdownlint-enable no-inline-html -->
