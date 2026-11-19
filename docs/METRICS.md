# Metrics

This page describes the exported metrics and what they mean.

Which metrics appear depends on your GPU, driver, operating system and collection backend (`nvml` or `exec`). For every metric both backends serve, the exported series shares the same metric name, base unit, label set and enum mapping, while `nvml` preserves native driver precision (bytes, milliwatts, and `1/16` degree Celsius `nvidia_smi_temperature_gpu` resolution on R620 and newer drivers via field ID `308`) and serves fields that `nvidia-smi` marks deprecated while the driver library still answers. The collection status metric differs by backend: `nvml` reports `nvidia_smi_nvml_return_code` in place of `nvidia_smi_command_exit_code`.

On top of that shared core, the `nvml` backend serves the driver-only metric families described below. The `demo` backend synthesizes the full `nvml` surface, see [CONFIGURE.md](CONFIGURE.md#demo-mode).

For complete real scrape outputs across captured GPUs and drivers, see [internal/integration/testdata](../internal/integration/testdata). The exporter also exposes the standard Go runtime, process and `promhttp` metric families.

## NVML-only metrics

These families come from the NVIDIA driver library (`libnvidia-ml` / `nvml.dll`) and are exported by the `nvml` and `demo` backends.

### Energy, PCIe and NVLink

- `nvidia_smi_energy_joules_total{uuid}` (counter): total energy consumed by
  the GPU in joules since the driver was last loaded. Always on in the NVML
  backend. Absent on GPUs that cannot report it (older than the Volta
  generation). The counter resets when the driver reloads or the GPU is
  reset, so read it through `rate()` or `increase()`.
- `nvidia_smi_pcie_throughput_tx_bytes_per_second{uuid}` and
  `nvidia_smi_pcie_throughput_rx_bytes_per_second{uuid}` (gauges): PCIe
  traffic transmitted and received by the GPU. On non-MIG Hopper and newer
  GPUs where GPM is supported, TX and RX throughput are exported by default
  over the inter-collection GPM window (metric IDs `20` and `21`) without
  blocking, starting from the second collection. On GPUs without whole-GPU
  GPM, pass `--collect.pcie-throughput` to enable the `GetPcieThroughput`
  fallback, which samples each direction over its own 20ms counter window
  (see [CONFIGURE.md](CONFIGURE.md)).
- `nvidia_smi_pcie_replays_total{uuid}` and
  `nvidia_smi_pcie_replay_rollovers_total{uuid}` (counters): PCIe data link
  layer replay and replay-rollover counts (`FI_DEV_PCIE_REPLAY_COUNTER` and
  `FI_DEV_PCIE_REPLAY_ROLLOVER_COUNTER`). Always on in the NVML backend and
  omitted when the driver reports `NVML_ERROR_NOT_SUPPORTED`. Both counters
  reset on GPU reset or driver reload, so read them through `rate()` or
  `increase()`.
- `nvidia_smi_pcie_errors_total{uuid, type}` (counter) and
  `nvidia_smi_pcie_l0_to_recovery_total{uuid}` (counter): PCIe AER error counts
  by severity (`type` in `correctable`, `non_fatal`, `fatal`) and PCIe
  L0-to-recovery link retraining transitions (`FI_DEV_PCIE_COUNT_*` and
  `FI_DEV_PCIE_L0_TO_RECOVERY_COUNTER`). Omitted individually when unsupported.
- `nvidia_smi_pcie_runtime_suspended_seconds_total{uuid}` and
  `nvidia_smi_pcie_runtime_active_seconds_total{uuid}` (counters): cumulative
  seconds the GPU PCIe device has spent runtime-suspended or runtime-active,
  read from Linux sysfs (`/sys/bus/pci/devices/<bus_id>/power/runtime_*_time`).
  Note that NVML queries wake a runtime-suspended GPU, so scrape intervals
  shorter than the driver's idle suspend delay keep the device active.
  Omitted on non-Linux hosts or when sysfs is unreadable.
- `nvidia_smi_nvlink_tx_bytes_total{uuid}` and
  `nvidia_smi_nvlink_rx_bytes_total{uuid}` (counters),
  `nvidia_smi_nvlink_links{uuid}` and `nvidia_smi_nvlink_active_links{uuid}`
  (gauges), plus `nvidia_smi_nvlink_recovery_events_total{uuid}` and
  `nvidia_smi_nvlink_recovery_failed_events_total{uuid}` (counters): aggregate
  NVLink data payload bytes (`FI_DEV_NVLINK_THROUGHPUT_DATA_TX` and
  `FI_DEV_NVLINK_THROUGHPUT_DATA_RX`, converted from KiB to bytes), total and
  active NVLink counts (`FI_DEV_NVLINK_LINK_COUNT` with NVSwitch fallback to
  `FI_DEV_NVSWITCH_CONNECTED_LINK_COUNT`, and per-link `FI_DEV_NVLINK_GET_STATE`
  counting `ACTIVE` links with fallback to `nvmlDeviceGetNvLinkState`), and
  GPU-wide NVLink 5 recovery event counts (summed across all links from
  `FI_DEV_NVLINK_COUNT_LINK_RECOVERY_EVENTS` and
  `FI_DEV_NVLINK_COUNT_LINK_RECOVERY_FAILED_EVENTS`). Always on in the NVML
  backend when `FI_DEV_NVLINK_LINK_COUNT > 0` or
  `FI_DEV_NVSWITCH_CONNECTED_LINK_COUNT > 0`, and omitted on GPUs without
  NVLink or when unsupported. Unbridged cards can report `nvlink_links > 0`
  with zero active links, and idle links can enter NVML's sleep state and drop
  out of `nvlink_active_links`, so alert on
  `nvlink_recovery_failed_events_total` rather than comparing
  `nvlink_active_links` against `nvlink_links`. All NVLink counters reset on
  GPU reset or driver reload, so read them through `rate()` or `increase()`.
- `nvidia_smi_c2c_links{uuid}` and `nvidia_smi_c2c_active_links{uuid}`
  (gauges): total and active Chip-to-Chip (C2C) link counts on Grace-Hopper,
  Grace-Blackwell, and GB10 systems (`FI_DEV_C2C_LINK_COUNT` and per-link
  `FI_DEV_C2C_LINK_GET_STATUS`). Omitted on GPUs without C2C.
- `nvidia_smi_perf_policy_violation_seconds_total{uuid, policy}` (counter) and
  `nvidia_smi_perf_policy_base_clocks_violation_seconds_total{uuid}` (counter):
  cumulative time in seconds the GPU was throttled by a performance policy
  (`policy` in `thermal`, `board_limit`, `reliability`) or ran below its base
  clock (`FI_DEV_PERF_POLICY_*`, converted from nanoseconds to seconds).
  Individual policies can overlap in time, so read each policy series on its
  own and use `perf_policy_base_clocks_violation_seconds_total` for total time
  below base clock.

### Fans, BAR1 memory, thermal thresholds, power and health

- `nvidia_smi_fan_speed_current_ratio{uuid, fan}` and
  `nvidia_smi_fan_speed_target_ratio{uuid, fan}` (gauges, typically `0.0` to `1.0`, may exceed `1.0`),
  `nvidia_smi_fan_speed_rpm{uuid, fan}` (gauge), and
  `nvidia_smi_fan_control_policy{uuid, fan}` (gauge, `0` automatic, `1`
  manual): per-cooler current speed ratio (`GetFanSpeed_v2`), target speed
  ratio (`GetTargetFanSpeed`), rotor speed in revolutions per minute
  (`nvmlDeviceGetFanSpeedRPM`), and control policy (`GetFanControlPolicy_v2`),
  labeled by zero-based cooler index `fan`. Omitted on passively cooled GPUs
  (`GetNumFans == 0`), and each individual metric is omitted when its driver
  call is unsupported on the card or driver. The GPU-level
  `nvidia_smi_fan_speed_ratio{uuid}` metric from the `fan.speed` query field
  is kept alongside them.
- `nvidia_smi_bar1_memory_total_bytes{uuid}`,
  `nvidia_smi_bar1_memory_used_bytes{uuid}` and
  `nvidia_smi_bar1_memory_free_bytes{uuid}` (gauges): BAR1 PCIe aperture
  memory capacity, usage, and free space in bytes (`GetBAR1MemoryInfo`).
- `nvidia_smi_temperature_threshold_shutdown_celsius{uuid}`,
  `nvidia_smi_temperature_threshold_slowdown_celsius{uuid}`,
  `nvidia_smi_temperature_threshold_memory_max_celsius{uuid}`,
  `nvidia_smi_temperature_threshold_gpu_max_celsius{uuid}` and
  `nvidia_smi_temperature_threshold_acoustic_celsius{uuid}` (gauges): thermal
  thresholds in degrees Celsius (`GetTemperatureThreshold`). Unsupported
  thresholds on a given card or driver are omitted individually.
- `nvidia_smi_temperature_tlimit_threshold_shutdown_celsius{uuid}`,
  `nvidia_smi_temperature_tlimit_threshold_slowdown_celsius{uuid}`,
  `nvidia_smi_temperature_tlimit_threshold_memory_max_celsius{uuid}` and
  `nvidia_smi_temperature_tlimit_threshold_gpu_max_celsius{uuid}` (gauges):
  signed T.Limit thermal offsets in degrees Celsius on Ada and newer GPUs
  (`FI_DEV_TEMPERATURE_*_TLIMIT`), comparable against
  `nvidia_smi_temperature_gpu_tlimit`. Omitted on earlier architectures.
- `nvidia_smi_power_source{uuid}` (gauge): active power source reported by
  `GetPowerSource`, `0` for AC power, `1` for battery, and `2` for an
  undersized power supply. Omitted when unsupported.
- `nvidia_smi_power_mizer_mode{uuid}` (gauge): active PowerMizer mode reported
  by `GetPowerMizerMode_v1` (`0` adaptive, `1` prefer max performance, `2`
  auto, `3` consistent performance). Omitted when unsupported.
- `nvidia_smi_repair_pending{uuid, type}` (gauge, `type` in `channel`, `tpc`)
  and `nvidia_smi_unrepairable_memory{uuid}` (gauge): hardware repair-pending
  flags (`GetRepairStatus`) and unrepairable memory status
  (`GetUnrepairableMemoryFlag_v1`), `1` when active and `0` when healthy.
  Omitted when unsupported.
- `nvidia_smi_virtualization_mode{uuid}` (gauge): virtualization mode reported
  by `GetVirtualizationMode` (`0` none, `1` passthrough, `2` vGPU, `3` host
  vGPU, `4` host vSGA). Omitted when unsupported.

### Whole-GPU GPM activity

- `nvidia_smi_graphics_activity_ratio{uuid}`,
  `nvidia_smi_sm_activity_ratio{uuid}`,
  `nvidia_smi_sm_occupancy_ratio{uuid}`,
  `nvidia_smi_tensor_activity_ratio{uuid}`,
  `nvidia_smi_dram_bandwidth_utilization_ratio{uuid}`,
  `nvidia_smi_integer_activity_ratio{uuid}`,
  `nvidia_smi_fp64_activity_ratio{uuid}`,
  `nvidia_smi_fp32_activity_ratio{uuid}`,
  `nvidia_smi_fp16_activity_ratio{uuid}`, plus
  `nvidia_smi_c2c_throughput_tx_bytes_per_second{uuid}` and
  `nvidia_smi_c2c_throughput_rx_bytes_per_second{uuid}` (gauges): fine-grained
  engine activity, precision pipe utilization (`0.0` to `1.0`), DRAM bandwidth
  utilization, and C2C throughput (on C2C-equipped GPUs) from NVML GPU
  Performance Monitoring (`nvmlGpmSampleGet`), computed over the window
  between the two most recent collections. Always on in the NVML backend and
  probed at runtime via `nvmlGpmQueryDeviceSupport`: present on Hopper and
  newer supported GPUs when MIG mode is disabled, and absent on Ada and
  earlier architectures (or when MIG mode is enabled, where the per-MIG GPM
  metrics below take over). Whole-GPU GPM uses the same two-sample window
  guards described below for MIG instances.

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
  instances hosting several compute slices. On pre-510 drivers lacking
  `GetMemoryInfo_v2`, the exporter falls back to `GetMemoryInfo` and omits
  `nvidia_smi_mig_memory_reserved_bytes`.
- `nvidia_smi_mig_{graphics_activity,sm_activity,sm_occupancy,tensor_activity,dram_bandwidth_utilization,integer_activity,fp64_activity,fp32_activity,fp16_activity}_ratio{uuid, gpu_instance_id}`
  and `nvidia_smi_mig_pcie_throughput_{tx,rx}_bytes_per_second{uuid, gpu_instance_id}`
  (gauges): the GPU instance's activity, computed over the window between
  the two most recent collections.

The activity metrics have a few details worth knowing:

- They appear from the second collection that sees a GPU instance, and only
  on GPUs whose driver supports the GPM interface (the Hopper generation and
  later). A100-class MIG gets inventory and memory only.
- The window is guarded on both ends. Collections less than 150ms apart
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

With `--collect.compute-apps-mig` (requires `--collect.compute-apps` or
`--collect.graphics-apps`) the per-process metrics additionally carry
`gpu_instance_id` and `compute_instance_id` labels, attributing each process
to its MIG instance (empty for processes on non-MIG GPUs). It is opt-in
because it changes the label set of the per-process series.

Container note: full visibility under MIG, including per-process metrics, requires a privileged container with `NVIDIA_MIG_MONITOR_DEVICES=all` and the host PID namespace. Root is not needed, the exporter runs as its non-root uid (checked on an H200). Unprivileged, with only the environment variable, the per-MIG inventory, memory and activity metrics still work while the GPU-level memory fields are omitted. On Kubernetes, privileged mode must be configured together with the rest of the security context, see the [chart README](../charts/nvidia-gpu-exporter/README.md).

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
- `nvidia_smi_mig_xid_errors_total{uuid, gpu_instance_id, compute_instance_id, xid}`
  (counter) and
  `nvidia_smi_mig_xid_last_timestamp_seconds{uuid, gpu_instance_id, compute_instance_id, xid}`
  (gauge): MIG-attributed XID event counter and receive timestamp, emitted
  alongside the GPU-level `nvidia_smi_xid_*` series when `nvmlEventSetWait_v2`
  reports a valid `gpuInstanceId` (other than `0xFFFFFFFF`, with
  `compute_instance_id` empty when `computeInstanceId` is `0xFFFFFFFF`).
- `nvidia_smi_xid_watcher_enabled` (gauge): `1` when the NVML XID event
  watcher is registered and active, or `0` when event registration is
  unsupported or unavailable (for example on Windows under the WDDM driver
  model). Lets alerts distinguish "zero XIDs observed" from "XID watching is
  inactive".

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

The default backend reads it from `nvidia-smi --version` and refreshes it if
`driver_version` changes across collections (falling back to `nvidia-smi -q`
on pre-550 drivers that do not recognize `--version`). The NVML backend asks
the library directly. When it cannot be read, the label value is empty.

## Enum-valued metrics

Many `nvidia-smi` fields report a state rather than a number. The exporter
maps those to a number so they can be scraped.

A field that is unavailable, reading a value like `N/A`, `[Not Supported]`
or `[Insufficient Permissions]`, is not exported. A value that is present but
not recognized (an unexpected or brand-new state) is also skipped rather than
guessed, and is logged so that the gap is visible.

Two-state fields become `1`/`0`. This covers every field whose value is
`Enabled`/`Disabled`, `Yes`/`No`, `Active`/`Not Active` or `valid`/`invalid`,
for example:

- `persistence_mode`, `accounting.mode`, `display_mode`, `display_active`
- `ecc.mode.current`/`.pending`, `mig.mode.current`/`.pending`
- `gsp.mode.current`/`.default`, `c2c.mode`
- `remapped_rows.failure`/`.pending`
- `reset_status.reset_required`, `reset_status.drain_and_reset_recommended`
- `inforom.checksum_validation`
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
- `nvidia_smi_fabric_status`: `0` Success, or the non-zero NVML error code
  reported for the GPU fabric registration (exported once `fabric_state`
  reaches `3` Completed, with non-zero error codes available on the `nvml`
  backend).
- `nvidia_smi_compute_mode`: `0` Default, `1` Exclusive Thread,
  `2` Prohibited, `3` Exclusive Process.
- `nvidia_smi_addressing_mode`: `0` None, `1` HMM, `2` ATS.
- `nvidia_smi_gom_current` and `nvidia_smi_gom_pending`: GPU Operation Mode,
  `0` All On, `1` Compute, `2` Low Double Precision.
- `nvidia_smi_platform_peer_type`: `0` Direct Connected,
  `1` Switch Connected.

All multi-state fields except `pstate` map to their native NVML enum integer.
`pstate` is the raw performance-state number.

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
set of series per process holding a compute or CUDA MPS context on a GPU. See
the [configuration reference](CONFIGURE.md#per-process-gpu-metrics) for the
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

With `--collect.compute-apps`, the exporter also emits up to six per-process
engine utilization gauges (`0.0` to `1.0`) on the same label set:

- `nvidia_smi_compute_app_sm_utilization_ratio`: SM (3D/compute) utilization
  ratio of the process over the driver's recent sampling window.
- `nvidia_smi_compute_app_memory_utilization_ratio`: memory bandwidth
  utilization ratio of the process over the driver's recent sampling window.
- `nvidia_smi_compute_app_encoder_utilization_ratio`: video encoder (NVENC)
  utilization ratio of the process over the driver's recent sampling window.
- `nvidia_smi_compute_app_decoder_utilization_ratio`: video decoder (NVDEC)
  utilization ratio of the process over the driver's recent sampling window.
- `nvidia_smi_compute_app_jpeg_utilization_ratio`: JPEG engine (NVJPG)
  utilization ratio of the process over the driver's recent sampling window.
- `nvidia_smi_compute_app_ofa_utilization_ratio`: Optical Flow Accelerator
  (OFA) utilization ratio of the process over the driver's recent sampling
  window.

Active processes that were idle during the sampling window report `0`. On
`nvml`, R550 and newer drivers read all six engines via
`nvmlDeviceGetProcessesUtilizationInfo` while older drivers fall back to
`nvmlDeviceGetProcessUtilization` and omit `jpeg` and `ofa`. On `exec`,
`nvidia-smi pmon -c 1` supplies the utilization percentages. On non-MIG GPUs,
`-` cells in columns present in the `pmon` header report `0`, while columns
absent from older `pmon` headers (such as pre-R550 `jpg` and `ofa`) are
omitted. When per-process utilization is unavailable (such as on MIG-enabled
GPUs, or inside a container without host PID visibility where `pmon` cannot
observe host PIDs), the utilization series are omitted while `*_app_info` and
`*_app_used_memory_bytes` are still served.

`--collect.graphics-apps` emits the matching series for processes holding a
graphics context on a GPU (`nvidia_smi_graphics_app_info`,
`nvidia_smi_graphics_app_used_memory_bytes`,
`nvidia_smi_graphics_app_sm_utilization_ratio`,
`nvidia_smi_graphics_app_memory_utilization_ratio`,
`nvidia_smi_graphics_app_encoder_utilization_ratio`,
`nvidia_smi_graphics_app_decoder_utilization_ratio`,
`nvidia_smi_graphics_app_jpeg_utilization_ratio`,
`nvidia_smi_graphics_app_ofa_utilization_ratio`, `nvidia_smi_graphics_apps`,
and `nvidia_smi_graphics_apps_last_collect_success`). A process that holds
both a compute and a graphics context appears in both families and shares the
same PID-level utilization reading.

### Workload attribution and Kubernetes joins

With `--collect.compute-apps-identity` (requires `--collect.compute-apps` or
`--collect.graphics-apps`, and Linux or the demo backend), the per-process
`*_app_info`, `*_app_used_memory_bytes`, and `*_app_*_utilization_ratio` series
additionally carry two best-effort workload identity labels read from
`/proc/<pid>/cgroup` on Linux (synthesized in `demo` mode, and requiring host
PID visibility when the exporter runs in a container):

- `container_id`: the lowercase 64-character hexadecimal container ID for
  processes running inside a Docker, containerd, CRI-O or Podman container, or
  empty when no recognized container cgroup could be read.
- `systemd_unit`: the innermost systemd unit in the process's cgroup path when
  that unit is a `.service` (for example `ollama.service`), or empty when
  `container_id` is set, when the innermost unit is a `.scope` (such as a login
  session or `init.scope`), or when it is the user manager
  (`user@<uid>.service`).

The two labels are mutually exclusive: a container running under a runtime
service (such as `docker.service` or a Podman Quadlet unit) reports
`container_id` and leaves `systemd_unit` empty. An empty label means no
recognized container or service cgroup was readable (for example, a process in
a login session, a process that exited before `/proc/<pid>/cgroup` was read, or
an inaccessible `/proc`). It is opt-in because it changes the label set of the
per-process series.

On Kubernetes, join `container_id` against `kube_pod_container_info` from
`kube-state-metrics` in the same cluster (after stripping its runtime scheme
prefix such as `containerd://`) to attribute per-process GPU memory (or engine
utilization via `nvidia_smi_compute_app_*_utilization_ratio`) to a pod and
container (returning series for matched Kubernetes containers):

```promql
nvidia_smi_compute_app_used_memory_bytes{container_id!=""}
  * on (container_id) group_left (namespace, pod, container)
    max by (container_id, namespace, pod, container) (
      label_replace(
        kube_pod_container_info{container_id!=""},
        "container_id", "$1", "container_id", "^[a-z0-9-]+://([0-9a-f]{64})$"
      )
    )
```

### Correlating with vLLM (pod attribution and energy per token)

When running `vLLM` (or another inference server that exports Prometheus
metrics) alongside `nvidia_gpu_exporter`, the same `container_id` join bridges
the GPU's `uuid` to the `vLLM` pod (`namespace`, `pod`). Once `uuid` is mapped
to `(namespace, pod)`, any per-GPU series (`nvidia_smi_power_draw_watts`,
`nvidia_smi_energy_joules_total`, `nvidia_smi_temperature_gpu`,
`nvidia_smi_dram_bandwidth_utilization_ratio`) can be grouped by pod or joined
with `vLLM` series:

```promql
max by (uuid, namespace, pod) (
  nvidia_smi_compute_app_info{container_id!=""}
    * on (container_id) group_left (namespace, pod)
      max by (container_id, namespace, pod) (
        label_replace(
          kube_pod_container_info{container_id!=""},
          "container_id", "$1", "container_id", "^[a-z0-9-]+://([0-9a-f]{64})$"
        )
      )
)
```

To compute GPU energy in joules per generated token over a 1-hour window on a
dedicated single-workload node, use the hardware energy counter in `nvml` mode:

```promql
sum(increase(nvidia_smi_energy_joules_total[1h]))
  / (sum(increase(vllm:generation_tokens_total[1h])) > 0)
```

In `exec` mode, where `nvidia_smi_energy_joules_total` is not available,
integrate sampled power draw over the same window (`1 W * 3600 s = 3600 J`):

```promql
sum(avg_over_time(nvidia_smi_power_draw_watts[1h]) * 3600)
  / (sum(increase(vllm:generation_tokens_total[1h])) > 0)
```

On nodes running multiple `vLLM` pods on dedicated GPUs, join the numerator
with the `(uuid, namespace, pod)` mapping above and aggregate both sides
`by (namespace, pod)`.

Keep three constraints in mind for clean attribution:

- Use the same lookback window (`[1h]` above) in both the numerator and the
  denominator.
- Filter zero-token windows (`> 0` on the denominator) so idle periods do not
  divide idle GPU baseload power by zero or near-zero token counts.
- `nvidia_smi_energy_joules_total` and `nvidia_smi_power_draw_watts` are
  per-GPU, and `nvidia_smi_compute_app_info` reflects whichever pod holds a
  CUDA context at query time. Per-pod energy attribution over a window like
  `[1h]` requires dedicated GPU assignment where that single pod held the GPU
  across the window (no time-slicing, pod replacement or other GPU process on
  the same physical GPU).
