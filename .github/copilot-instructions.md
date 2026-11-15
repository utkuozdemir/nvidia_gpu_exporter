# Copilot instructions

Read `AGENTS.md` in the repository root first. It is the project guide, and everything below is only the short version for when you have not opened it.

- Compatibility is cumulative across every driver generation and operating system: evolve to cover new driver outputs, fields and symbols alongside existing ones rather than replacing old paths.
- Every backend uses its capabilities in full and never skips a metric or coarsens precision or granularity because another backend cannot provide it. Whenever two backends expose the same data, they must share identical metric names, base units, label sets and enum mappings.
- This exporter's metric names and labels are a public contract. Renaming or relabeling an established series silently breaks users' dashboards and alerts, so treat existing names as frozen. New families outside `nvidia-smi` CSV headers follow upstream Prometheus metric, base-unit and label naming conventions.
- The expected `.metrics` files under `internal/integration/testdata/` are generated, not hand-edited. Regenerate them with the integration suite's `-update` flag and review the diff.
- The Helm chart's `README.md` is generated and must never be edited directly: its prose lives in `README.md.gotmpl`, and its values reference comes from the comments in `values.yaml`. Regenerate with helm-docs after changing either. A new key in `values.yaml` also needs an entry in `values.schema.json`, which rejects unknown keys.
- The Grafana dashboards under `docs/grafana/` are the source of truth, and the copies shipped in the chart must stay byte-identical (CI enforces it). See `docs/grafana/README.md` for the query conventions, several of which fail silently when violated. Whenever a metric is added, changed or widened across backends, evaluate the Grafana dashboards as a whole: check for broken queries, outdated panel descriptions or units, and meaningful panel additions or improvements that stay clean across all backends and hardware shapes.
- Verify with `go test ./...` and the `task lint:*` target relevant to what you changed; CI runs the full `task lint` set.
- Commits follow the conventional-commit format (`type(scope): imperative description`) with a body explaining what changed and why, and carry a DCO sign-off. Do not add AI attribution of any kind, such as "generated with" lines or AI co-author trailers.
