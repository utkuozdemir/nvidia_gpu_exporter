# Grafana dashboards

Two dashboards live here, and these files are the source of truth for both:

- `dashboard.json`: the per-GPU detail dashboard, published as
  [grafana.com 14574](https://grafana.com/grafana/dashboards/14574).
- `dashboard-overview.json`: the multi-GPU overview, published as
  [grafana.com 25547](https://grafana.com/grafana/dashboards/25547), which
  drills down into the detail dashboard.

Each carries the `nvidia_gpu_exporter` tag and a "Related dashboards" link, so
they find each other in Grafana once both are installed.

The Helm chart ships copies under `charts/nvidia-gpu-exporter/dashboards/`, and CI
compares them byte for byte, so a change here must be mirrored there in the same
commit. Publishing a new revision to grafana.com is a manual step, separate from
the release pipeline.

`task lint:dashboard` runs
[grafana/dashboard-linter](https://github.com/grafana/dashboard-linter) in strict
mode. Rule exclusions live in `.lint`, each with a written reason; the bar for
adding one is that the rule conflicts with the dashboards' long-published design.

The dashboards are normally authored in the Grafana UI and exported back into
these files, but editing the JSON directly is fine too.

## The images

`dashboard.png` and `dashboard-overview.png` are what the README shows. They
are generated, never screen-captured by hand:

```bash
task screenshots     # or hack/compose/screenshots.sh
```

That brings the dev stack up with a `grafana-image-renderer` sidecar, points it
at row-expanded copies of these dashboards, and writes both files here. The
renderer photographs a collapsed row as a collapsed row, which is why the copies
exist; `render-dashboard.sh --screenshot` derives them, so there is no second
copy of a dashboard to keep in sync.

The pictures are of the NVML surface of the simulated datacenter machine, the
only one that populates the MIG and XID panels. Refresh them when a dashboard
changes, and read the image diff: every run photographs live demo data, so the
pixels differ each time even when nothing else did. The `screenshots` workflow
runs the same command and opens a pull request with the result.

## Query and panel conventions

Each of these came out of a panel that looked fine and was wrong, so treat them
as correctness rules rather than style. Most fail silently when violated: the
panel still renders, it just shows the wrong thing or nothing at all. Verify any
query change against the local dev stack (`hack/compose`), including its empty
and failed-collection cases, before committing.

- **Enrichment joins for legends** take the form
  `(expr) * on(uuid) group_left(index, name) nvidia_smi_gpu_info{...}`.
  The parentheses are load-bearing, because `*` binds tighter than `+` and `or`.
- **Table targets need an absence sentinel.** Every table target ORs an arm of
  the form `... nvidia_smi_gpu_info * 0 - 1`, and `-1` maps to an n/a-style cell.
  Without it, a totally absent series makes the join drop whole columns. The
  sentinel arm must be aggregated down to the same labels as the joined left
  side, otherwise it doubles the series.
- **MIG joins must tolerate heterogeneous compute instances.** A GPU instance
  hosting several compute instances hard-errors any join carrying the profile
  label. Strip the compute-instance prefix with `label_replace` before
  aggregating.
- **Panels reading NVML-only families need an `unless`-guarded uuid-only arm**,
  or they go blank instead of degrading when a collection fails.
- **Fields some hardware honestly lacks get one of two absence idioms.** A
  single-value panel ORs a `sum(nvidia_smi_gpu_info{...}) * 0 - 1` arm and maps
  `-1` to "N/A", which keeps three states apart: a reading, N/A on a card that
  is alive but lacks the field (a passively cooled card's fan, whole-GPU
  utilization on a MIG-enabled card), and "No data" when collection itself
  failed. A timeseries only gets a `noValue` text, because the sentinel would
  draw as a flat line there; that blurs the failed-collection case, which is
  acceptable on a graph whose neighbours all blank out with it. The texts state
  what is known, not a cause the panel cannot prove: "No XID data", never "no
  XIDs happened", because the exec backend records none regardless.
- **Never put a benign `noValue` on a panel whose query already carries a
  `gpu_info` fallback arm.** Such a panel is empty *only* when collection is
  dead, so a calm label there would caption the one state that must look
  broken. Check the query before adding a text, not the panel type.
- **State timelines** use fixed/`text` color mode with no thresholds key; the
  value mappings carry the band colors.
- **Multi-GPU timeseries** use `palette-classic`, because dynamically-named
  series cannot take a custom palette.

## Compatibility

The dashboards have a large installed base, which constrains two things:

- **Metric names and labels are a frozen contract.** See `AGENTS.md` for the
  full rules; in short, a renamed series silently breaks strangers' dashboards.
- **The instance-selector template variable is not named `instance`,** and cannot
  be renamed. Every bookmarked URL carries the current name. The dashboard-linter
  rule that wants `instance` is excluded in `.lint` rather than obeyed.
