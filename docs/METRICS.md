# Metrics

This page describes the exported metrics and what they mean.

Which metrics appear depends on your GPU, driver and operating system. The
query fields are auto-discovered by default (`AUTO`), so the exporter picks
up new fields and exposes them on a best-effort basis.

For a complete, real example of what a scrape looks like, see the expected
outputs the integration tests compare against, under
[internal/integration/testdata](../internal/integration/testdata). There is
one file per captured GPU, driver and load state. Each is the exact
`nvidia_smi_*` output the exporter produces for it, so they stay current by
construction.

The exporter additionally exposes the standard Go runtime, process and
`promhttp` metric families, which are not shown there.

For every query field the NVML backend and the default backend both serve,
the metric is identical. The one exception is the collection status metric:
the NVML backend reports `nvidia_smi_nvml_return_code` in place of
`nvidia_smi_command_exit_code`.

On top of that shared core, the NVML backend serves the NVML-only families
described in the next section. The demo backend approximates the NVML
backend's surface with synthetic data, see
[CONFIGURE.md](CONFIGURE.md#demo-mode).

## NVML-only metrics

Some readings exist in the driver library but not in the `nvidia-smi` query
interface, so only the NVML backend can export them.

### Energy and PCIe throughput

- `nvidia_smi_energy_joules_total{uuid}` (counter): total energy consumed by
  the GPU in joules since the driver was last loaded. Always on in the NVML
  backend. Absent on GPUs that cannot report it (older than the Volta
  generation). The counter resets when the driver reloads or the GPU is
  reset, so read it through `rate()` or `increase()`.
- `nvidia_smi_pcie_throughput_tx_bytes_per_second{uuid}` and
  `nvidia_smi_pcie_throughput_rx_bytes_per_second{uuid}` (gauges): PCIe
  traffic transmitted and received by the GPU. Opt-in via
  `--collect.pcie-throughput` because of its collection cost, see
  [CONFIGURE.md](CONFIGURE.md). The driver samples each direction over its
  own 20ms counter window, so the two values are consecutive samples, not a
  simultaneous pair.

### MIG instances

On GPUs with MIG mode enabled, the NVML backend also exports per-MIG-instance
metrics. Nothing to configure, they appear when MIG instances exist.

- `nvidia_smi_mig_info{uuid, mig_uuid, gpu_instance_id, compute_instance_id, profile}`
  (constant `1`): the identity of each MIG device. `uuid` is the parent
  GPU's, so MIG series join with all per-GPU series. `mig_uuid` is the MIG
  device's own.
- `nvidia_smi_mig_memory_{total,used,free,reserved}_bytes{uuid, gpu_instance_id}`
  (gauges): the GPU instance's memory. The framebuffer belongs to the GPU
  instance and is shared by its compute instances, so memory is reported
  once per GPU instance. Labeling it per MIG device would double-count on
  instances hosting several compute slices.
- `nvidia_smi_mig_{graphics_activity,sm_activity,sm_occupancy,tensor_activity}_ratio{uuid, gpu_instance_id}`
  and `nvidia_smi_mig_pcie_throughput_{tx,rx}_bytes_per_second{uuid, gpu_instance_id}`
  (gauges): the GPU instance's activity, computed over the window between
  the two most recent collections.

The activity metrics have a few details worth knowing:

- They appear from the second collection that sees a GPU instance, and only
  on GPUs whose driver supports the GPM interface (the Hopper generation and
  later). A100-class MIG gets inventory and memory only.
- The window is guarded on both ends. Collections less than a second apart
  keep serving the previous values over the previous window. After a gap
  longer than ten minutes the sampling starts over, and the next collection
  emits nothing for that instance, like a first sight.
- The utilization is attributed per GPU instance, not per MIG device. A GPU
  instance hosting several compute instances reports one set of values.
  Joining `mig_info` on `(uuid, gpu_instance_id)` maps them back to the MIG
  devices.
- With several independent scrapers the window follows whoever collected
  last. For stable windows, pair this with `--collect.interval`.
- Destroying a GPU instance and recreating a different shape under the same
  numeric id is detected, and the activity sampling starts over. Recreating
  the exact same shape in the same placement is indistinguishable, because
  MIG uuids are deterministic. So the one window spanning such a swap may
  blend the two instances before self-correcting.

With `--collect.compute-apps-mig` (requires `--collect.compute-apps` and the
NVML backend) the per-process metrics additionally carry `gpu_instance_id`
and `compute_instance_id` labels, attributing each process to its MIG
instance (empty for processes on non-MIG GPUs). It is opt-in because it
changes the label set of the per-process series.

Container note: like the per-process metrics under MIG, full function needs
generous privileges. The exporter container may need to run privileged with
`NVIDIA_MIG_MONITOR_DEVICES=all` and share the host PID namespace. MIG
inventory and memory worked unprivileged in testing.

### XID errors

The NVML backend also watches for XID errors, the driver's numbered error
events (a stuck kernel, an ECC failure, a fallen-off-the-bus GPU). These are
invisible to `nvidia-smi` and to the query-field metrics. They only surface
through driver events or the kernel log.

- `nvidia_smi_xid_errors_total{uuid, xid}` (counter): XID error events
  observed since the exporter started. A series appears when its first
  event arrives. History from before the exporter started cannot be
  replayed. The counters live outside the collection pipeline, so they stay
  visible while collections fail, which is exactly when XIDs happen.
- `nvidia_smi_xid_last_timestamp_seconds{uuid, xid}` (gauge): when the most
  recent event was received by the exporter. The driver events carry no
  timestamp of their own.

For alerting, prefer the timestamp and filter to an explicit code allowlist:

```text
time() - nvidia_smi_xid_last_timestamp_seconds{xid=~"48|62|64|74|79|95|119|120"} < 300
```

This fires for the reset/reboot-class XIDs in the last five minutes,
including a series' very first event.

Alerting on every code is noise. Many XIDs are application faults (13, 31,
43) or informational (63, 92), with no operator action.

A rate-based expression like `increase(nvidia_smi_xid_errors_total[5m]) > 0`
also misses a series' first event, because Prometheus never observed the
zero before it. The Helm chart ships ready-made rules built the way described
above.

Nothing needs configuring. On setups where the driver does not support event
registration, the families simply stay empty.

## CUDA version

Both backends stamp the CUDA version the installed driver supports onto
`nvidia_smi_gpu_info` as the `cuda_version` label. This is the version of
the CUDA API the driver carries, not an installed CUDA toolkit.

The default backend reads it from `nvidia-smi --version` once at startup. The
NVML backend asks the library directly. When it cannot be read, the label
value is empty.

## Enum-valued metrics

Many `nvidia-smi` fields report a state rather than a number. The exporter
maps those to a number so they can be scraped.

A field that is unavailable, reading a value like `N/A`, `[Not Supported]`
or `[Insufficient Permissions]`, is not exported. A value that is present but
not recognized (an unexpected or brand-new state) is also skipped rather than
guessed, and is logged so that the gap is visible.

Two-state fields become `1`/`0`. This covers every field whose value is
`Enabled`/`Disabled`, `Yes`/`No` or `Active`/`Not Active`, for example:

- `persistence_mode`, `accounting.mode`, `display_mode`, `display_active`
- `ecc.mode.current`/`.pending`, `mig.mode.current`/`.pending`
- `gsp.mode.current`/`.default`, `c2c.mode`
- `remapped_rows.failure`/`.pending`
- every `clocks_event_reasons.*` throttle flag (`gpu_idle`, `hw_slowdown`,
  `sw_power_cap`, and so on)

Which fields appear depends on the GPU and driver, because query fields are
auto-discovered.

Multi-state fields carry the state as their integer value:

- `nvidia_smi_pstate`: the performance state, `0` (`P0`, maximum performance)
  through `15` (`P15`, minimum). Lower means busier, so this is not a load
  gauge.
- `nvidia_smi_gpu_recovery_action`: `0` None (healthy), `1` GPU Reset,
  `2` Node Reboot, `3` Drain P2P, `4` Drain and Reset. A value `> 0` means the
  driver recommends a recovery action, so it is a good "GPU is unhealthy"
  alert.
- `nvidia_smi_fabric_state`: `0` Not Supported, `1` Not Started,
  `2` In Progress, `3` Completed. Only present on GPUs that join an NVLink
  fabric.
- `nvidia_smi_compute_mode`: `0` Default, `1` Exclusive Thread,
  `2` Prohibited, `3` Exclusive Process.

The last three map to their native NVML enum integer. `pstate` is the raw
performance-state number.

`gpu_recovery_action` and `fabric_state` only appear when the hardware and
driver report them (the recovery action needs a recent driver). Their absence
from an exporter's output is expected, not a bug.

## What a scrape looks like

An excerpt, from a single-GPU machine. Each per-GPU series is labeled by the
GPU `uuid`, so it joins with `nvidia_smi_gpu_info`, which carries the
identity labels:

```text
# HELP nvidia_smi_gpu_info A metric with a constant '1' value labeled by gpu uuid, name, driver_model_current, driver_model_pending, vbios_version, driver_version, pci_bus_id, serial, compute_cap, pci_sub_device_id, index, cuda_version.
# TYPE nvidia_smi_gpu_info gauge
nvidia_smi_gpu_info{uuid="df6e7a7c-7314-46f8-abc4-b88b36dcf3aa",name="NVIDIA GeForce RTX 2080 SUPER",driver_version="471.11",cuda_version="11.4",pci_bus_id="00000000:71:00.0",index="0",...} 1
# HELP nvidia_smi_utilization_gpu_ratio utilization.gpu [%]
# TYPE nvidia_smi_utilization_gpu_ratio gauge
nvidia_smi_utilization_gpu_ratio{uuid="df6e7a7c-7314-46f8-abc4-b88b36dcf3aa"} 0.42
# HELP nvidia_smi_memory_used_bytes memory.used [MiB]
# TYPE nvidia_smi_memory_used_bytes gauge
nvidia_smi_memory_used_bytes{uuid="df6e7a7c-7314-46f8-abc4-b88b36dcf3aa"} 7.06740224e+08
# HELP nvidia_smi_power_draw_watts power.draw [W]
# TYPE nvidia_smi_power_draw_watts gauge
nvidia_smi_power_draw_watts{uuid="df6e7a7c-7314-46f8-abc4-b88b36dcf3aa"} 28.07
# HELP nvidia_smi_temperature_gpu temperature.gpu
# TYPE nvidia_smi_temperature_gpu gauge
nvidia_smi_temperature_gpu{uuid="df6e7a7c-7314-46f8-abc4-b88b36dcf3aa"} 34
```

Alongside the per-GPU series, the exporter reports its own collection health.
These carry no `uuid` label, and they are what alerting on the exporter
itself should use:

```text
# HELP nvidia_smi_last_collect_success Whether the most recent collection succeeded (1) or not (0)
# TYPE nvidia_smi_last_collect_success gauge
nvidia_smi_last_collect_success 1
# HELP nvidia_smi_last_collect_success_timestamp_seconds Unix timestamp of the most recent successful collection
# TYPE nvidia_smi_last_collect_success_timestamp_seconds gauge
nvidia_smi_last_collect_success_timestamp_seconds 1.751475457e+09
# HELP nvidia_smi_last_collect_duration_seconds Duration of the most recent collection
# TYPE nvidia_smi_last_collect_duration_seconds gauge
nvidia_smi_last_collect_duration_seconds 0.055694458
# HELP nvidia_smi_failed_scrapes_total Number of failed collections
# TYPE nvidia_smi_failed_scrapes_total counter
nvidia_smi_failed_scrapes_total 0
# HELP nvidia_smi_command_exit_code Exit code of the most recent nvidia-smi run
# TYPE nvidia_smi_command_exit_code gauge
nvidia_smi_command_exit_code 0
```

Note that `nvidia_smi_failed_scrapes_total` counts failed *collections*, not
failed scrapes. The two came apart when background collection was added, and
the name is kept for compatibility.

The full set is much larger and varies by hardware. See
[internal/integration/testdata](../internal/integration/testdata) for
complete outputs across every captured GPU.

## Per-process metrics (opt-in)

With `--collect.compute-apps` enabled, the exporter additionally emits one
set of series per process holding a compute context on a GPU. See the
[configuration reference](CONFIGURE.md#per-process-gpu-metrics) for the
caveats around containers, Windows and MIG.

```text
# HELP nvidia_smi_compute_app_info A metric with a constant '1' value labeled by the identity of a process with a compute context on a GPU.
# TYPE nvidia_smi_compute_app_info gauge
nvidia_smi_compute_app_info{pid="10291",process_name="/root/tools/memhog",uuid="00000000-0000-0000-0000-000000000000"} 1
nvidia_smi_compute_app_info{pid="10309",process_name="./gpu_burn",uuid="00000000-0000-0000-0000-000000000000"} 1
# HELP nvidia_smi_compute_app_used_memory_bytes GPU memory used by the process. Absent when the driver cannot report it (e.g. Windows WDDM).
# TYPE nvidia_smi_compute_app_used_memory_bytes gauge
nvidia_smi_compute_app_used_memory_bytes{pid="10291",process_name="/root/tools/memhog",uuid="00000000-0000-0000-0000-000000000000"} 2.690646016e+09
nvidia_smi_compute_app_used_memory_bytes{pid="10309",process_name="./gpu_burn",uuid="00000000-0000-0000-0000-000000000000"} 2.9605494784e+10
# HELP nvidia_smi_compute_apps Number of processes with a compute context on the GPU.
# TYPE nvidia_smi_compute_apps gauge
nvidia_smi_compute_apps{uuid="00000000-0000-0000-0000-000000000000"} 2
# HELP nvidia_smi_compute_apps_last_collect_success Whether the most recent per-process collection succeeded (1) or not (0)
# TYPE nvidia_smi_compute_apps_last_collect_success gauge
nvidia_smi_compute_apps_last_collect_success 1
```
