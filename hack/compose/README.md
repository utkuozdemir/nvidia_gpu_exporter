# Local dev stack

A one-command stack for developing the exporter and its Grafana dashboards on a
machine with **no GPU**. It runs two flavors side by side:

- **exec flavor** — the real exporter against the fake nvidia-smi binary
  (`cmd/fake-nvidia-smi`): `node1` (an eight-GPU consumer box on `fake.yaml`,
  one card deliberately sick) and `node2` (a two-GPU passive L40S box on
  `fake2.yaml`), scraped by `prometheus` (:9090).
- **demo flavor** — the exporter's built-in demo backend
  (`--collect.backend=demo`), which serves the NVML-superset surface (MIG
  instances, XID error counters, energy, PCIe throughput) with synthetic
  data: `demo1` (two H200s with a MIG topology and XID history, `demo1.yaml`)
  and `demo2` (a single consumer card, `demo2.yaml`), scraped by
  `prometheus-demo` (:9091).

Grafana carries both Prometheuses as data sources, so the dashboards' data
source dropdown flips between the two worlds. The provisioned dev dashboards
preselect the demo flavor, the richer surface where dashboard work happens.

## Run

```bash
./hack/compose/up.sh          # builds and starts; Ctrl-C to stop
./hack/compose/up.sh -d       # detached
```

Then open:

- Grafana: <http://localhost:3000> (anonymous admin, no login) — the **Nvidia GPU
  Metrics** and **Nvidia GPU Overview** dashboards are provisioned, pointed at
  the demo Prometheus; switch the *Data source* dropdown for the exec flavor.
- Prometheus (exec): <http://localhost:9090>, (demo): <http://localhost:9091>
- Exporter metrics: <http://localhost:9835/metrics> (node1),
  <http://localhost:9836/metrics> (node2), <http://localhost:9837/metrics>
  (demo1), <http://localhost:9838/metrics> (demo2)

Stop and wipe the throwaway state (Prometheus/Grafana volumes):

```bash
cd hack/compose && docker compose down -v
```

The compose code, provisioning, and the fake/demo configs are committed and
maintained; the running containers and their data volumes are disposable.
Existing volumes upgrade in place (the exec data source keeps its historical
name and uid, so provisioning upserts it); if Grafana ever fails to start
over a conflicting manually-added data source, `docker compose down -v`
resets the throwaway state.

`render-dashboard.sh` (run by `up.sh`) needs `python3` on the host, in
addition to Docker.

## Make the data interesting

### exec flavor

`fake/fake.yaml` drives the fake. `fluctuate: true` jitters everything that
naturally moves (utilization, temperature, power, clocks, fan, memory) around
the captured values, `gpus:` simulates eight cards from the single-GPU capture
(the last one deliberately sick, to preview the health states in the GPU
dropdown), and `overrides:` drive the states the jitter does not cover, like
the throttle flags. The fake is invoked fresh on every scrape, so **editing
`fake/fake.yaml` changes the next scrape with no restart** (give it one scrape
interval). To preview a specific panel state, pin a field:

```yaml
overrides:
  gpu_recovery_action: "Reset"   # drive the health tile
  temperature.gpu: 95            # drive the temperature threshold color
```

Field names are nvidia-smi query fields; see `internal/captures/README.md`. To
drive a different card, change `capture:` to any embedded capture name.
`fake2.yaml` drives the second exporter the same way; its passive L40S cards
report no fan speed, which exercises the "metric absent" paths on the
dashboards.

### demo flavor

`demo/demo1.yaml` and `demo/demo2.yaml` use the same format plus a
demo-specific `extras:` block (MIG topology, XID events, PCIe ranges; the
full reference is the demo mode section in `docs/CONFIGURE.md`). The demo
backend re-reads its file on every collection cycle, so edits apply live
here too; an edit resets the synthesized counters, like a driver reload.
demo1's MIG topology deliberately hosts two compute instances on one GPU
instance: any dashboard join that forgets to pre-aggregate `mig_info` per
GPU instance shows doubled values here before it ships.

## Iterate on the dashboards

The dashboards are authored in the Grafana UI and exported to
`docs/grafana/dashboard.json` (single-GPU detail, grafana.com 14574) and
`docs/grafana/dashboard-overview.json` (multi-GPU comparison). Those files
select their data source through a template variable; `render-dashboard.sh`
copies them into the provisioning directory, flipping `editable` on and
preselecting the demo data source (the published artifacts themselves stay
data-source-neutral).

Loop: edit the JSON under `docs/grafana/` (or edit in the UI and export it
there), run `./hack/compose/render-dashboard.sh`, and Grafana reloads within a
few seconds. Keep each `docs/grafana/*.json` and its copy under
`charts/nvidia-gpu-exporter/dashboards/` byte-identical.
