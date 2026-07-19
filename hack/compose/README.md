# Local dev stack

A one-command stack for developing the exporter and its Grafana dashboards on
a machine with **no GPU**. It simulates two machines and serves each through
**both** backend flavors, so the dashboards can compare the same machine's
exec surface against its NVML surface and every visible difference is a real
surface difference, not a different-simulated-hardware artifact:

- **machine "consumer"** (`machines/consumer.yaml`): an eight-GPU consumer inference
  box, one card deliberately sick (health banner, throttle timeline,
  temperature alerts).
- **machine "datacenter"** (`machines/datacenter.yaml`): two healthy H200s with a MIG
  topology (one GPU instance deliberately hosting two compute instances, the
  live probe for dashboard join cardinality) and an XID error history.

Flavors: `exec-consumer`/`exec-datacenter` run the real exec pipeline against the fake
nvidia-smi binary, scraped by `prometheus` (:9090); `nvml-consumer`/`nvml-datacenter` run
the demo backend, which serves the NVML surface (MIG, XID, energy, PCIe) on
top of the same table, scraped by `prometheus-demo` (:9091). Both
Prometheuses label the same machine with the same instance name (`consumer`,
`datacenter`), so flipping the Grafana data source dropdown keeps the node selection
pointed at the same machine.

The provisioned dev dashboards preselect the NVML data source, the richer
surface where dashboard work happens.

## Run

```bash
./hack/compose/up.sh          # builds and starts; Ctrl-C to stop
./hack/compose/up.sh -d       # detached
```

Then open:

- Grafana: <http://localhost:3000> (anonymous admin, no login) — the **Nvidia GPU
  Metrics** and **Nvidia GPU Overview** dashboards are provisioned; switch the
  *Data source* dropdown between "Prometheus - NVML" and "Prometheus - Exec"
  to compare the two surfaces of the selected machine.
- Prometheus (exec): <http://localhost:9090>, (NVML): <http://localhost:9091>
- Alertmanager: <http://localhost:9093> — the chart's alert rules evaluated
  against both flavors (see "Verify the alert rules")
- Exporter metrics: <http://localhost:9835/metrics> (exec-consumer),
  <http://localhost:9836/metrics> (exec-datacenter), <http://localhost:9837/metrics>
  (nvml-consumer), <http://localhost:9838/metrics> (nvml-datacenter)

Stop and wipe the throwaway state (Prometheus/Grafana volumes):

```bash
cd hack/compose && docker compose down -v
```

The compose code, provisioning and the machine configs are committed and
maintained; the running containers and their data volumes are disposable.
Volumes created by earlier revisions of this stack (different data source or
service names) make Grafana or Prometheus trip over stale state; run
`docker compose down -v` once to reset.

`render-dashboard.sh` (run by `up.sh`) needs `python3` on the host, in
addition to Docker. `render-rules.sh` (also run by `up.sh`) needs only
Docker: it runs helm and yq from pinned images.

## Verify the alert rules

`render-rules.sh` renders the Helm chart's PrometheusRule into
`prometheus/rules/` with **every** rule force-enabled and all `for:`
durations shortened to 30s, so the stack always evaluates exactly what the
chart ships and alerts appear within a minute. Both Prometheuses evaluate
the same rules and send to the one Alertmanager; their `backend` external
label (`exec`/`demo`) keeps the two flavors' alerts apart there. After
changing the chart's rules, re-run `./hack/compose/render-rules.sh` and
Prometheus picks the change up on restart (`docker compose restart
prometheus prometheus-demo`).

What fires out of the box, all on the sick consumer card unless noted:

- `NvidiaGpuRecoveryActionNeeded`, `NvidiaGpuUncorrectableEccErrors`,
  `NvidiaGpuRowRemapFailure`, `NvidiaGpuRowRemapPending`,
  `NvidiaGpuRetiredPagesPending`, `NvidiaGpuThermalSlowdown`,
  `NvidiaGpuTemperatureHigh` — from the sick card's pinned overrides, on
  both backends.
- `NvidiaGpuXidCritical` (Xid 79) and `NvidiaGpuXidWarning` (Xid 94) — from
  the datacenter machine's seeded XID history, demo backend only (the exec
  surface honestly has no XID metrics). Xid 13 is also seeded and
  deliberately fires nothing (application fault).
- Healthy cards fire nothing: the alertable slowdown flags are pinned 0 on
  them, and only cosmetic flags flip randomly.

The exporter self-health alerts need a broken exporter, kept out of the
default stack:

- `NvidiaGpuExporterCollectionFailing`: start the failure box with
  `docker compose --profile broken up -d exec-broken` — its fake nvidia-smi
  exits non-zero from boot (instance `broken` on the exec Prometheus).
- `NvidiaGpuExporterCollectionStale`: edit `machines/consumer.yaml` live and
  add `exit: 15` at the top level; collection starts failing fast, the last
  success timestamp ages, and the stale alert follows the failing one.
  Remove the line to recover. A from-boot broken exporter never emits the
  stale timestamp, which is why stale needs this healthy-then-fail
  transition instead of the broken profile.
- `NvidiaGpuExporterCollectionSlow`: add `delay: 2s` instead. Careful with
  larger values: the delay applies per fake-nvidia-smi invocation, the
  compute-apps query doubles it, and past the stack's 5s scrape timeout the
  scrape dies before it can report a slow duration (the dev render lowers
  the slow threshold to 2s for exactly this headroom reason).
- `NvidiaGpuSoftwareThermalSlowdown` and `NvidiaGpuPowerBrake`: flip the
  machine-level `clocks_event_reasons.sw_thermal_slowdown` or
  `.hw_power_brake_slowdown` override in `machines/consumer.yaml` from `0`
  to `1`; revert to recover.
- `NvidiaGpuMissing`: shrink the `gpus:` list of a machine live (e.g. 8 to
  7 entries); the alert compares against the recent 6h maximum. The
  all-GPUs-gone form cannot be driven here (the fake refuses a zero-GPU
  config, which breaks collection and trips the healthy-collection gate
  instead); it is covered by a promtool unit test in `hack/alerts/`.

## What "same machine, two surfaces" means (and its limits)

Both flavors of a machine read the same YAML file: same capture, same GPU
identities (the generated uuids are derived from the GPU index, identically
in both flavors), same overrides, same jitter bands. Values still jitter
independently per flavor (each invocation draws its own randomness), so the
two data sources show the same machine under the same conditions, not
tick-identical numbers. Structural differences that remain are real:

- The NVML flavor serves the extras families (`mig_*`, `xid_*`,
  `energy_joules_total`, `pcie_throughput_*`) and `nvml_return_code`; the
  exec flavor serves `command_exit_code` and no extras. That is the honest
  difference between the surfaces.
- The NVML flavor synthesizes the extras (the demo backend approximates the
  real nvml backend's surface); the sick-GPU drama and the whole table are
  identical in kind on both sides.

## Make the data interesting

`machines/*.yaml` drive everything. `fluctuate: true` jitters what naturally
moves (utilization, temperature, power, clocks, fan, memory) around the
captured values, `gpus:` replicates the capture into several cards with
stable identities and per-GPU overrides (consumer's last card carries the sick
overrides), and `overrides:` pin the states jitter does not cover. Both
flavors re-read the file on every scrape/collection cycle, so **editing a
machine file changes the next scrape of both its flavors, no restart** (give
it one scrape interval; an edit resets the NVML flavor's synthesized
counters, like a driver reload). To preview a specific panel state, pin a
field:

```yaml
overrides:
  gpu_recovery_action: "Reset"   # drive the health tile
  temperature.gpu: 95            # drive the temperature threshold color
```

Field names are nvidia-smi query fields; see `internal/captures/README.md`.
To drive a different card, change `capture:` to any embedded capture name
(the demo backend embeds the H200 and RTX 4080 SUPER captures; the fake
binary embeds the full corpus). The `extras:` block (MIG topology, XID
events, PCIe ranges) is read by the demo backend and ignored by the fake —
the full reference is the demo mode section in `docs/CONFIGURE.md`.

## Iterate on the dashboards

The dashboards are authored in the Grafana UI and exported to
`docs/grafana/dashboard.json` (single-GPU detail, grafana.com 14574) and
`docs/grafana/dashboard-overview.json` (multi-GPU comparison). Those files
select their data source through a template variable; `render-dashboard.sh`
copies them into the provisioning directory, flipping `editable` on and
preselecting the NVML data source (the published artifacts themselves stay
data-source-neutral).

Loop: edit the JSON under `docs/grafana/` (or edit in the UI and export it
there), run `./hack/compose/render-dashboard.sh`, and Grafana reloads within
a few seconds. Keep each `docs/grafana/*.json` and its copy under
`charts/nvidia-gpu-exporter/dashboards/` byte-identical.
