# Roadmap

What the project intends to do, and not do, in the next year or so. This is a side project, so there are no dates. Last reviewed 2026-08-30.

## Doing

- Get the NVML backend out of experimental. It needs testing across driver versions and GPU generations, and reports from people who run it, good or bad, are what moves it. A test comparing both backends on real hardware exists, but so far CI only compiles it and it has never been executed. Getting it running regularly on a GPU box is the missing piece.
- Keep growing the capture corpus. Every recorded `nvidia-smi` output from a GPU, driver or OS the tests have not seen extends the test matrix. This is the easiest way to contribute.

## Looking into

- Structured `nvidia-smi` output instead of CSV ([#423](https://github.com/utkuozdemir/nvidia_gpu_exporter/issues/423)). Less fragile to parse, if it is complete and not too slow.
- Per-field query cost and a tuning guide ([#424](https://github.com/utkuozdemir/nvidia_gpu_exporter/issues/424)), so people know which fields to exclude on a slow setup.
- A turnkey monitoring stack, exporter plus a metrics backend plus Grafana, preconfigured ([#425](https://github.com/utkuozdemir/nvidia_gpu_exporter/issues/425)). Rough idea, no commitment. Licensing needs a look first.

## Not doing

- Datacenter fleet management features: profiling counters, NVLink topology, GPU Operator integration. DCGM and the GPU Operator own that space and do it better. This exporter targets what `nvidia-smi` can reach and heavier tooling cannot or will not.
- Other GPU vendors. AMD or Apple GPUs would be a separate project, not a backend here.
- Renaming or relabeling established metrics. The metric names are a public contract with dashboards and alerts built on them. New things get new names.
- A web UI. Grafana is the UI, and the two official dashboards are maintained here.
