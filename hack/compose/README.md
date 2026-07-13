# Local dev stack

A one-command stack for developing the exporter and its Grafana dashboards on a
machine with **no GPU**. It runs the real exporter against the fake nvidia-smi
(`cmd/fake-nvidia-smi`), scrapes it with Prometheus, and serves the dashboards
in Grafana with live, moving values. Two exporter instances play two "nodes"
(an eight-GPU consumer box on `fake.yaml`, a two-GPU passive L40S box on
`fake2.yaml`), so per-node filtering and multi-GPU comparison are exercisable.

## Run

```bash
./hack/compose/up.sh          # builds and starts; Ctrl-C to stop
./hack/compose/up.sh -d       # detached
```

Then open:

- Grafana: <http://localhost:3000> (anonymous admin, no login) — the **Nvidia GPU
  Metrics** and **Nvidia GPU Overview** dashboards are provisioned and already
  pointed at Prometheus.
- Prometheus: <http://localhost:9090>
- Exporter metrics: <http://localhost:9835/metrics> (node one),
  <http://localhost:9836/metrics> (node two)

Stop and wipe the throwaway state (Prometheus/Grafana volumes):

```bash
cd hack/compose && docker compose down -v
```

The compose code, provisioning, and `fake.yaml` are committed and maintained; the
running containers and their data volumes are disposable.

## Make the data interesting

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

## Iterate on the dashboards

The dashboards are authored in the Grafana UI and exported to
`docs/grafana/dashboard.json` (single-GPU detail, grafana.com 14574) and
`docs/grafana/dashboard-overview.json` (multi-GPU comparison). Those files
select their data source through a template variable, which resolves to
this stack's sole Prometheus on its own, so `render-dashboard.sh` simply
copies them into the provisioning directory.

Loop: edit the JSON under `docs/grafana/` (or edit in the UI and export it
there), run `./hack/compose/render-dashboard.sh`, and Grafana reloads within a
few seconds. Keep each `docs/grafana/*.json` and its copy under
`charts/nvidia-gpu-exporter/dashboards/` byte-identical.
