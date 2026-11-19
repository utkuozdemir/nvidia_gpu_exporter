# Roadmap

What the project intends to do, and not do, in the next year or so. This is a side project, so there are no dates. Last reviewed 2026-10-25.

## Doing

- Keep growing the capture corpus. Every recorded `nvidia-smi` output from a GPU, driver or OS the tests have not seen extends the test matrix. This is the easiest way to contribute.

## Looking into

- Structured `nvidia-smi` output instead of CSV ([#423](https://github.com/utkuozdemir/nvidia_gpu_exporter/issues/423)). Less fragile to parse, if it is complete and not too slow.
- Per-field query cost and a tuning guide ([#424](https://github.com/utkuozdemir/nvidia_gpu_exporter/issues/424)), so people know which fields to exclude on a slow setup.
- A turnkey monitoring stack, exporter plus a metrics backend plus Grafana, preconfigured ([#425](https://github.com/utkuozdemir/nvidia_gpu_exporter/issues/425)). Rough idea, no commitment. Licensing needs a look first.

## Not doing

- Datacenter fleet management features: deep hardware profiling counters (beyond the NVML GPM activity ratios runtime-probed via `nvmlGpmQueryDeviceSupport` on Hopper and newer supported devices, which are absent on Ada and earlier architectures), per-lane NVLink topology matrices, and GPU Operator integration. DCGM and the GPU Operator own that space and do it better. This exporter targets what `nvidia-smi` and lightweight NVML queries can reach and heavier tooling cannot or will not.
- Other GPU vendors. AMD or Apple GPUs would be a separate project, not a backend here.
- Renaming or relabeling established metrics. The metric names are a public contract with dashboards and alerts built on them. New things get new names.
- A web UI. Grafana is the UI, and the two official dashboards are maintained here.
