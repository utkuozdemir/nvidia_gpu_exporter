# Local dev stack

A one-command stack for developing the exporter and its Grafana dashboard on a
machine with **no GPU**. It runs the real exporter against the fake nvidia-smi
(`cmd/fake-nvidia-smi`), scrapes it with Prometheus, and serves the dashboard in
Grafana with live, moving values.

## Run

```bash
./hack/compose/up.sh          # builds and starts; Ctrl-C to stop
./hack/compose/up.sh -d       # detached
```

Then open:

- Grafana: <http://localhost:3000> (anonymous admin, no login) — the **Nvidia GPU
  Metrics** dashboard is provisioned and already pointed at Prometheus.
- Prometheus: <http://localhost:9090>
- Exporter metrics: <http://localhost:9835/metrics>

Stop and wipe the throwaway state (Prometheus/Grafana volumes):

```bash
cd hack/compose && docker compose down -v
```

The compose code, provisioning, and `fake.yaml` are committed and maintained; the
running containers and their data volumes are disposable.

## Make the data interesting

`fake/fake.yaml` drives the fake. `fluctuate: true` jitters everything that
naturally moves (utilization, temperature, power, clocks, fan, memory) around
the captured values, `gpus:` simulates four cards from the single-GPU capture
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

## Iterate on the dashboard

The dashboard is authored in the Grafana UI and exported to
`docs/grafana/dashboard.json` (the published artifact, grafana.com 14574). That
file selects its data source through a template variable, which resolves to
this stack's sole Prometheus on its own, so `render-dashboard.sh` simply
copies it into the provisioning directory.

Loop: edit `docs/grafana/dashboard.json` (or edit in the UI and export it there),
run `./hack/compose/render-dashboard.sh`, and Grafana reloads within a few
seconds. Keep `docs/grafana/dashboard.json` and
`charts/nvidia-gpu-exporter/dashboards/nvidia-gpu-metrics.json` byte-identical.
