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
addition to Docker.

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
