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

This is a simple exporter that uses the `nvidia-smi(.exe)` binary to collect,
parse and export metrics. Since it only needs `nvidia-smi`, it also works on
Windows - no Docker or Linux required.

It can also skip `nvidia-smi` entirely and read the metrics straight from
the driver library. See [the NVML backend](#try-the-native-nvml-backend)
below.

## Use cases

- Consumer and prosumer GPUs (GeForce/RTX), where the datacenter tooling
  exposes little and `nvidia-smi` is often the only uniform source of
  utilization, memory, power and temperature
- Small Kubernetes clusters, edge boxes and homelabs that want GPU metrics
  without installing the NVIDIA GPU Operator stack
- Virtualized or restricted setups (vGPU guests, MIG slices, locked-down containers)
  where the deeper GPU counters are not exposed but `nvidia-smi` still answers
- Mixed fleets of old and new cards that need one exporter that behaves the
  same everywhere
- Gaming rigs, for watching your GPU stats on a dashboard while you play

If you run datacenter cards on Kubernetes with the GPU Operator already
installed, [DCGM-exporter](https://github.com/NVIDIA/dcgm-exporter) is
probably the better fit; this exporter aims at the cases above.

## Highlights

- Will work on any system that has `nvidia-smi(.exe)?` binary - Windows, Linux, MacOS... No C bindings required
- Doesn't even need to run on the monitored machine: can be configured to execute `nvidia-smi` command remotely
- Auto-discovery of the metric fields `nvidia-smi` can expose (future-compatible)
- Optional per-process GPU metrics: see which process uses how much GPU memory
- Optional background collection: run `nvidia-smi` on a timer instead of on every scrape
- Comes with its own Grafana dashboards: a [per-GPU detail](https://grafana.com/grafana/dashboards/14574) one and a [multi-GPU overview](https://grafana.com/grafana/dashboards/25547)

## Try the native NVML backend

On Linux, the exporter can skip `nvidia-smi` and read the metrics directly
from the NVIDIA driver library (NVML). Every metric the default backend
serves stays identical in name, labels and value, so existing dashboards and
alerts keep working. On top of that it adds families `nvidia-smi` cannot
provide: per-MIG-instance metrics, XID error counters, a total energy
counter and PCIe throughput. The official Grafana dashboards have panels for
all of these, which sit empty on the default backend and light up on this
one.

It ships as its own release flavor that already defaults to this backend:
grab a `-nvml` archive from the
[releases page](https://github.com/utkuozdemir/nvidia_gpu_exporter/releases),
or use a `-nvml` image tag:

```bash
docker run -d \
  --name nvidia_gpu_exporter \
  --restart unless-stopped \
  --gpus all \
  -e NVIDIA_DRIVER_CAPABILITIES=utility \
  -p 9835:9835 \
  utkuozdemir/nvidia_gpu_exporter:latest-nvml
```

It is marked experimental mainly because it needs more mileage across driver
versions and GPU generations. If you try it, [open an
issue](https://github.com/utkuozdemir/nvidia_gpu_exporter/issues) about how
it went, good or bad. That is what will get it past the experimental label.
See [CONFIGURE.md](docs/CONFIGURE.md#experimental-native-nvml-backend) for
the full backend comparison and current limits.

## Try it without a GPU

Demo mode serves realistic synthetic metrics, including the NVML-only
families, with no GPU, driver or even Linux required:

```bash
nvidia_gpu_exporter --collect.backend demo
```

By default it simulates two H200 GPUs with fluctuating values, a MIG topology
and an XID error history. The simulated setup is configurable; see
[CONFIGURE.md](docs/CONFIGURE.md).

## Visualization

There are two official Grafana dashboards, and they link to each other in Grafana:

- [Nvidia GPU Metrics](https://grafana.com/grafana/dashboards/14574) (ID `14574`),
  the per-GPU detail view.
- [Nvidia GPU Overview](https://grafana.com/grafana/dashboards/25547) (ID `25547`),
  which compares all GPUs of a node side by side and drills down into the
  detail dashboard.

Import either by ID in Grafana (*Dashboards* - *New* - *Import*), or enable
`grafanaDashboard` in the Helm chart to get both provisioned automatically. The
JSON is also in this repository under [docs/grafana](docs/grafana).

Here's how they look:

![Nvidia GPU Metrics](https://raw.githubusercontent.com/utkuozdemir/nvidia_gpu_exporter/main/docs/grafana/dashboard.png)

![Nvidia GPU Overview](https://raw.githubusercontent.com/utkuozdemir/nvidia_gpu_exporter/main/docs/grafana/dashboard-overview.png)

## Installation

You can install it from plain binaries, deb/rpm packages, winget, Docker
images or the [Helm chart](charts/nvidia-gpu-exporter).
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

### Contribute a GPU capture

The exporter parses `nvidia-smi` output, which differs across GPU models,
driver versions and operating systems. The test corpus already covers a good
range of hardware, but a capture from a setup it hasn't seen yet, say a new
GPU model or a brand-new driver, is still a welcome contribution and takes
one command:

```bash
./internal/captures/collect.sh          # add --load for an under-load sample too
```

It needs only `nvidia-smi`, `bash`, and the standard core utilities (`awk`,
`sed`, ...), runs read-only, and masks identifiers (GPU UUID, serial, hostname)
by default. It writes one `.txt` file: commit it and open a PR, or attach it to
an issue. See [internal/captures/README.md](internal/captures/README.md).

## Star History

<!-- markdownlint-disable no-inline-html -->
<a href="https://github.com/utkuozdemir/nvidia_gpu_exporter/stargazers">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="https://raw.githubusercontent.com/utkuozdemir/star-charts/main/charts/utkuozdemir/nvidia_gpu_exporter/dark.svg" />
    <img alt="Star history of utkuozdemir/nvidia_gpu_exporter" src="https://raw.githubusercontent.com/utkuozdemir/star-charts/main/charts/utkuozdemir/nvidia_gpu_exporter/light.svg" />
  </picture>
</a>
<!-- markdownlint-enable no-inline-html -->
