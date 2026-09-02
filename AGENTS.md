# nvidia_gpu_exporter — agent guide

This is the project's knowledge base for humans and agents working in this repository.
Maintain it as you go: whenever you learn something durable about how this repo works, add it here.
It is not append-only, so fix or delete anything that becomes wrong or outdated.

Keep it timeless.
Two rules keep it from rotting: **prefer pointers over copies**, and **prefer invariants over inventories**.
Never restate a flag list, a metric list, a chart value, a file count, a version number or the current state of some in-flight work.
Those live in the code and the docs, they change constantly, and a stale copy here is worse than no copy.
What belongs here is the map, and the reasoning that is not written down anywhere else.

`CLAUDE.md`, `GEMINI.md` and `.github/copilot-instructions.md` point at this file, so this is the one place to edit.
The Copilot file additionally inlines a few of the rules below, because its consumer injects that file verbatim instead of following the pointer; keep those copies in sync.

Markdown note: `task lint:assets` runs `markdownlint` over every tracked markdown file except the generated chart README, this one included, so it has to pass.
Keep one sentence per line and the first line as the single top-level heading.

## What this repo is

A Prometheus exporter for NVIDIA GPUs, distributed as a single static Go binary.
By default it runs `nvidia-smi`, parses its CSV output and exports the result, which is why it works anywhere that binary works (Linux, Windows, macOS, containers, WSL2) with no C bindings and no NVIDIA userspace of its own.

The distribution surface is wide for a project this size: archives, native packages, container images on more than one registry, a Helm chart, Windows package manifests, a systemd unit, a native Windows service, and the Grafana dashboards.
`.goreleaser.yml` and `.github/workflows/release.yml` are the authority on what is published and where.
Almost all of it is produced by the release pipeline from this repository, with one exception: the dashboards are published to grafana.com by hand.

The niche is deliberate.
DCGM and the NVIDIA GPU Operator own the datacenter fleet-management case.
This exporter aims at everything `nvidia-smi` can reach and heavier tooling cannot or will not: consumer and prosumer cards, small clusters and edge boxes, virtualized and locked-down environments, mixed old-and-new fleets, and remote scraping over a wrapper command.
When a change would only make sense for a large managed datacenter fleet, it is probably the wrong project for it.

## The three collection backends

Everything about the exporter's shape follows from there being three ways to obtain a reading.

`exec` is the default and the only one available on every platform.
It runs `nvidia-smi` as a subprocess and parses the CSV.
It is the only backend that can honor a custom command, which is what makes remote scraping over ssh and privilege wrappers like `sudo` work.
It is also the strongest isolation against a misbehaving driver, because a wedged subprocess can be killed and an in-process driver call cannot.

`nvml` reads the driver library directly through go-nvml, and is still labelled experimental.
It requires cgo and Linux, so it exists only as a separate release flavor whose default backend is flipped at link time.
On top of the query-field metrics it shares with the exec backend, it serves families only the driver library can provide.
Its query-field vocabulary is a compiled catalog, not runtime discovery, so it is not automatically a superset of what exec can reach.
The catalog explicitly defers fields it has no verified mapping for, and a field a new driver adds appears under exec's discovery first.
`internal/nvmlnative/catalog.go` holds both the catalog and the deferral list, and the drift tests force adding a newly-seen field to one or the other as an explicit decision.

`demo` serves synthetic data with no GPU, no driver and no Linux.
It is not a fourth code path: it runs the real exec pipeline against an in-process fake `nvidia-smi`, then synthesizes the NVML-only families on top.
So it is pure Go, works on any platform, and stays faithful to the real backends' quirks almost for free.

Two invariants hold this together, and both are easy to break by accident:

- For every query field both backends serve, they must emit identical metrics, down to the label sets and values.
  Users switch between the backends and their dashboards and alerts must keep working.
  The nvml catalog exists precisely to guarantee this, and it stores the exact header strings `nvidia-smi` prints, not paraphrases of them.
  Declining to serve a field is allowed; serving it differently is not.
- The demo backend targets the nvml surface, not the exec one, closely enough that dashboards and alerts can be developed against it.
  It approximates that surface without replicating it, and the differences are documented in the demo mode section of `docs/CONFIGURE.md`.
  The one most likely to mislead: demo always serves the PCIe throughput family, while the real nvml backend serves it only when asked.

Flag combinations that a chosen backend cannot honor are startup errors rather than silent ignores.
A user passing a custom command with the nvml backend means something the backend cannot do, and failing loudly is the only honest answer.

## Code map

- `cmd/nvidia_gpu_exporter`: the binary.
  Thin: process lifecycle, signal handling, and the Windows service and install commands behind build tags.
  Everything real lives behind `internal/app`.
- `cmd/fake-nvidia-smi`: the fake `nvidia-smi` as a standalone binary, with the whole capture corpus embedded.
- `internal/app`: the single entry point behind both the binary and the in-process tests.
  Flag parsing and validation, backend wiring, metric registration, HTTP serving.
  Tests drive the real entry, not a reimplementation of it.
- `internal/collect`: collection scheduling and health accounting, backend-agnostic.
  `Live` collects synchronously and shares one in-flight run between concurrent scrapes; `Cached` collects on a timer and serves the latest result.
  The `Snapshot` type keeps *data* and *health* strictly separate, and health is read from explicit booleans, never inferred from an error being non-nil.
  A failed collection publishes no GPU data instead of serving the last known good reading, in both modes, so a stale value can never be mistaken for a live one.
  The auxiliary families (per-process, and the nvml extras) fail softly instead: they go absent without failing the collection.
- `internal/exporter`: turns a snapshot into Prometheus metrics.
  Conditionally-described families follow one precedent: a disabled feature contributes no descriptors at all, so `Describe` and `Collect` stay consistent.
- `internal/nvidiasmi`: the exec backend: command splitting, field resolution, CSV parsing, value transformation, per-process queries.
- `internal/nvmlnative`: the nvml backend, guarded by `linux && cgo` build tags with an unavailable stub for every other target.
  Holds the field catalog, the MIG and XID paths, and the drift tests.
- `internal/demo`: the demo backend's configuration and its synthesizers for the NVML-only families.
- `internal/fakesmi`: the fake `nvidia-smi` implementation, shared by the standalone binary and the demo backend.
- `internal/captures` and `internal/capture`: the recorded corpus and its parser.
- `internal/demodata`: the small curated capture subset the demo backend embeds.
- `internal/integration`: the end-to-end suite.

## The capture corpus and how testing works

This is the most unusual part of the repository, and the part most often misread.

The exporter parses text whose shape varies by GPU model, driver version, operating system and load state.
Owning that hardware is impossible, so the project records real `nvidia-smi` output into self-contained capture files and treats them as the test substrate.
`internal/captures/README.md` documents the format, the masking rules and how contributors add one; read it before touching anything in that area.

The captures are executable.
The fake `nvidia-smi` replays a capture verbatim and answers arbitrary field subsets by projecting columns out of the recorded CSV, with no baked-in knowledge of GPUs or fields.
The integration suite then runs the real exporter against the fake for every capture in the corpus and compares the scraped output against a checked-in expected file.
Contributing a capture therefore extends the test matrix automatically, and a new capture fails the suite until its expected output is generated and reviewed.

Consequences to keep in mind before changing anything here:

- The expected `.metrics` files are compared byte for byte and are regenerated with the suite's `-update` flag, never hand-edited.
  Always read the resulting diff: it is the review step, and it is the only place where a change in exported output becomes visible.
- `.gitattributes` pins the corpus captures and expected outputs to LF.
  This is load-bearing, not cosmetic.
  A CRLF conversion on a Windows checkout would break the entire suite.
- The full corpus must never reach the shipped binary; only the curated demo subset may.
  A test asserts this by inspecting the binary's dependency graph, because the failure mode is silent size growth, not a broken build.
- The GPU-hardware parity test is behind a build tag and never runs in CI, but CI compiles it, so it cannot rot between the rare sessions where real hardware is available.
- The nvml backend's drift tests check its catalog and its required driver symbols against the recorded captures, so a driver that renames or withdraws something is caught offline.

### Fuzzing

The corpus covers the driver output that has been seen; fuzz targets cover the output that has not.
That gap is real here, because the exec backend parses whatever a driver, a platform or a user's wrapper command prints, and a panic in a parser takes the whole exporter down.

Every fuzz target asserts a property rather than just the absence of a panic, and the properties are the contracts callers already rely on: a parsed table is safe to index by column, a transformed value is finite, a discovered field name survives being joined into a query, a derived metric name is registrable.
`task test:fuzz` drives the search; `task test` replays the seed corpora, so a fixed bug stays fixed without anyone running a fuzzer.
Targets live in `fuzz_test.go` next to the package they cover, or `fuzz_internal_test.go` when they need unexported access, which is the repo's convention for internal-package tests and what the `testpackage` linter allows.

Two rules keep the targets honest, both learned by getting them wrong first:

- Assert at the layer that owns the invariant, not the layer below it. A metric name is only required to be registrable where all the names are built together, because that is where a collision can be seen and where a single bad descriptor would otherwise fail every scrape.
- Constrain a target to the input domain the code actually receives. Handing the fake's projection an invocation shape the fake never routes there produces failures that describe the test, not the code.

## Metrics and their compatibility contract

Exported metric names are a public contract, and breaking one is the most expensive mistake available in this repo.
The Grafana dashboard has a large installed base and there are alerting rules in the wild, so a renamed or relabeled series breaks strangers' monitoring silently.
Treat every established name as frozen unless there is an explicit decision otherwise.

Some specifics that regularly surprise people:

- Metric names derive from the returned header strings `nvidia-smi` prints, not from the query field names.
  That is why the nvml catalog stores those header strings byte-exactly, and why a driver renaming a header renames a metric.
- The `nvidia_smi_` prefix names the data schema, not the collection mechanism.
  It stays as-is in every backend, including the ones that never run `nvidia-smi`.
- The failed-collection counter's name says "scrapes" and counts collections.
  It has been a misnomer since background collection decoupled the two, and it stays, because the migration cost lands on users and the benefit is cosmetic.
- Health metrics are backend-neutral except the collection status one, which differs by backend on purpose.
  Reporting an NVML status under a metric named after a process exit code would silently change an established metric's meaning.
- Adding labels to an existing family changes series identity, so it goes behind an opt-in flag.
  The MIG attribution labels on the per-process metrics are the precedent to follow.
- Fields that report a state rather than a number are mapped to numbers.
  An unavailable value is not exported at all; an unrecognized value is also not exported, but is logged, because guessing at a brand-new driver state is worse than a visible gap.
- A derived metric name that is unusable costs the whole scrape, not one series, because the exporter registers as a single collector on every scrape and the registry rejects a bad descriptor wholesale.
  So a field whose returned name yields an empty name, or a name another field or one of the exporter's own families already owns, is dropped with a logged error instead.
  When several fields contend for one name, *all* of them are dropped: which field owns a derived name is not knowable here, and publishing one field's reading under a contested name would put a wrong value on an established series.
  The fixed family names live in one list in `internal/exporter`, pinned by a test against what the exporter actually describes, so a new family cannot be added without reserving its name.
- Every field list, whether the user wrote it or auto-detection discovered it, is deduplicated and has the identity fields appended before it is queried.
  Cells are assigned to fields positionally, so a duplicate would be queried twice and emit two samples for one series, failing the whole scrape.

`docs/METRICS.md` carries the reference and the per-family semantics; keep it in sync when the surface changes.

## Grafana dashboards

There are two: a per-GPU detail dashboard and a multi-GPU overview that drills down into it.
Both are published on grafana.com.

They are authored in the Grafana UI and exported to `docs/grafana/`, which is the source of truth.
The Helm chart ships copies, and CI compares them byte for byte, so the two locations must be updated together.
Publishing to grafana.com is a manual step outside the release pipeline.

`docs/grafana/README.md` carries the query and panel conventions the dashboards depend on.
Read it before changing any panel query: several of those conventions fail silently when violated.

The images the README shows are generated by `task screenshots`, not captured by hand.
Grafana's own image renderer does the headless part, so there is no browser automation anywhere in this repo, and a capture is an HTTP GET.
The one thing that renderer will not do is open a collapsed row, which is why the capture points at row-expanded copies derived from the published dashboards rather than at the dashboards themselves.
Every run photographs live demo data, so the pixels always differ: refresh the images when a dashboard changes, and review the image diff.

`task lint:dashboard` runs grafana's dashboard-linter in strict mode.
Rule exclusions live next to the dashboards, each with a written reason, and the bar for adding one is that the rule conflicts with the dashboard's long-published design.
The instance-selector template variable in particular is not named `instance` and cannot be renamed: every bookmarked URL carries the current name, so the linter rule is excluded instead of obeyed.

## Helm chart

The chart deploys a DaemonSet and is the primary Kubernetes install path.

Its central design decision: GPU access comes from the NVIDIA container runtime, injected via a runtime class and the NVIDIA environment variables, and the exporter never requests an `nvidia.com/gpu` resource.
The device plugin allocates whole GPUs exclusively, so a monitoring pod that requested one would take it away from real workloads.
Anything that reintroduces a GPU resource request is a bug.

Other things that are easy to trip over:

- Chart and app versions are stamped from the release tag; the in-repo versions are placeholders.
  The chart major is deliberately the app major plus one, a legacy of the chart's history that must be preserved by the release job.
- The chart README is assembled by helm-docs and CI fails if it is out of date, so never edit `README.md` directly.
  Its prose lives in `README.md.gotmpl`; only the values reference is generated, from the comments in `values.yaml`.
  Edit whichever of the two is appropriate, then regenerate.
- `values.schema.json` validates the values strictly and rejects unknown keys.
  A new value subtree added without a matching schema entry survives review and then fails chart linting and installation, so the two change together.
- The security context values deliberately skip the defaulted-lookup convention below, so a `--reuse-values` upgrade keeps the old empty ones and stays unhardened.
  That is the safe direction: backfilling them would hand a `runAsNonRoot` guarantee to anyone still pinning an older root image tag and break their pods at upgrade time.
- The alert rules are values-driven, and `hack/alerts/verify.sh` renders and unit-tests them without a cluster.
  It also simulates an upgrade that reuses a previous release's values, because Helm does not backfill new chart defaults on `--reuse-values`; that is why the templates use defaulted lookups instead of direct value references, and why new value subtrees must stay nil-safe.
- Alert rules ship with corrective actions in their annotations, and the verification script asserts the load-bearing ones survive rewording.

## Building, testing and linting

`Taskfile.yml` is the entry point and mirrors what CI runs.
`task lint` covers everything: Go, module tidiness, markdown, shell scripts, the dashboards, and the release configuration.
`task fmt` applies the formatters.
The individual `task lint:*` targets are the ones to reach for while iterating, since the full set needs several external tools installed.

The Go toolchain version is pinned in `go.mod` and CI reads it from there, so there is one place to change it.
Linting is `golangci-lint` configured to enable linters by default and disable a short, individually-justified list; new code is expected to satisfy it instead of accumulating suppressions.

GitHub CodeQL scans the repository through the default setup, which is configured in the repository settings rather than a workflow file, so nothing in the checkout records it.
CI additionally runs a goreleaser snapshot on every change.
That is intentional: it validates the packaging inputs (the systemd unit, the package scriptlets, the archive layouts) in CI instead of letting them fail for the first time during a real release.
It builds the container images for every platform without publishing them, which is what gates Renovate's automerged base-digest updates, and deliberately skips only the signing and SBOM stages, so those two paths are exercised only by a real release.

## Release pipeline

Releases are cut by pushing a version tag, which triggers the release workflow; the Taskfile has a target that derives the next version from the commit history and pushes the tag.
The tag is signed, and the workflow's first step refuses a tag that GitHub does not report as verified, so an unsigned tag fails before anything is built or published.
A tag ruleset makes release tags immutable (no update, no deletion), so a signature cannot be reused by re-pointing a tag. The ruleset does not check the tag signature itself, GitHub's signature rule looks at commits, not tag objects, so the workflow check is the enforcement.
The release task only tags the tip of the main branch, and the workflow refuses a tagged commit that is not on the main branch.

The pipeline builds two binary flavors from the same source: the fully static default build, and the cgo nvml flavor.
The release builds are reproducible on purpose: no build paths in the binaries, and the commit time as every timestamp, so the same commit yields the same bytes.
Keep new build steps deterministic.
Keeping the two apart is a real invariant.
The packages and the primary container image explicitly pin themselves to the static build, because a cgo binary silently reaching the deb, the rpm or the main image would ship something that cannot run where those artifacts are expected to run.
The nvml flavor is published as its own archive and as a suffixed image tag.

Release artifacts are signed two ways.
The GPG key signs the checksums file, which transitively covers every binary, archive and package, and it also signs the packaged Helm chart's provenance file.
Cosign signs the container images and the OCI chart keyless, tied to the release workflow's identity.
The release job verifies that the imported signing key matches the fingerprint the chart advertises, and fails loudly if it does not.
`hack/generate-signing-key.sh` documents and performs key generation and rotation.

The chart is published twice: to GHCR as an OCI artifact, and to a Helm repository on the `gh-pages` branch.
Both are expected to stay available; dropping either breaks existing users.

The container images sit on a distroless glibc base, the smallest base worth maintaining.
They cannot be libc-less: the NVIDIA container runtime injects `nvidia-smi` and the driver library from the host, both dynamically linked against glibc, so the image must provide a libc and its loader even though the exporter binary itself may be static.
This was verified empirically on real hardware through both injection mechanisms (CDI and the legacy nvidia runtime), and on a libc-less base the injected `nvidia-smi` fails to exec.
A hand-assembled libc-only rootfs would shave a few more megabytes but means owning the libc copy and its security tracking forever, so that option is deliberately written off.
The cgo nvml flavor links against the glibc of the release build runner, so the base's glibc must be at least as new as the runner's, an invariant to re-check on any base swap or runner upgrade.
The base carries no version tags, so it is pinned by digest and Renovate keeps the digest fresh, gated by CI building the images.
There is no shell in the image: derived images cannot use shell-form `RUN`, and wrapper commands need an image that copies the exporter binary onto a fuller base (documented in the install guide).
The images run as uid 65534, and the `USER` directive has to stay numeric: the kubelet cannot verify a `runAsNonRoot` guarantee against a name-based image user and refuses to start the container, which is also why the base stays the plain digest-pinned image rather than a `nonroot` tag variant whose form is not ours to control.
Executing a binary that exists in the image still works, e.g., `docker exec <container> nvidia-smi`, and fuller live debugging uses ephemeral debug containers (`kubectl debug`, or a `--pid=container:...` helper container under plain Docker).

One packaging footgun: the nvml image tag suffix parses as a semver pre-release.
Strict semver therefore sorts the flavored tag *below* the plain one of the same version, while tooling that sorts lexically or by push time may pick it instead, so automation consuming these tags needs an explicit filter either way.

## Developing without a GPU

In increasing order of setup:

- Run the exporter against the fake `nvidia-smi` binary via the custom command flag, replaying any capture in the corpus.
  Good for parser and field work.
- Run the exporter with the demo backend.
  Good for anything that needs the NVML-only families, and the only option that exercises them at all without hardware.
- Bring up the compose stack under `hack/compose`, which simulates several machines and serves each through both backend flavors, with Prometheus, Grafana and Alertmanager wired up.
  This is the environment for dashboard and alert work; its README documents how to drive specific states and make specific alerts fire.

The simulated machine configurations are re-read on every collection cycle, so editing one changes the next scrape with no restart.

## Conventions

- Commits follow the conventional-commit format: a `type(scope): imperative description` title plus a body explaining what changed and why.
  The changelog is generated by grouping commits on that type, so the type is functional, not decorative.
- History on the default branch stays linear.
  Integrate by rebase or squash, never by merge commit, and rebase branches onto the default branch rather than merging it into them.
- Commits carry a DCO sign-off.
- Nothing carries AI attribution: no "generated with" lines, no AI co-author trailers, in commits, pull requests or anywhere else.
- Documentation is split by audience: the README orients, `docs/` carries installation, configuration and metrics reference, and community files live under `.github/`.
  Deep, area-specific documentation lives next to the thing it documents, not in `docs/`.
- `.bestpractices.json` at the repository root holds the answers to the OpenSSF Best Practices badge questionnaire.
  Their site reads it from the default branch and proposes the answers in the form, so a change to how the project works (tests, releases, reporting) is reflected there, not in the web form. The live entry on bestpractices.dev and this file must be updated together.
- Dependencies are updated by Renovate, configured to merge qualifying updates as branches without opening pull requests; `.renovaterc.json` is the authority on which update classes qualify.
  Branch automerge only works while the default branch has no rule requiring a pull request before merging, so if dependency pull requests start appearing for passing updates, that rule is the thing to look for.
  Required status checks are fine and are what actually gate those merges.

## Reference pointers

- `README.md`: what the project is and who it is for.
- `docs/INSTALL.md`, `docs/CONFIGURE.md`, `docs/METRICS.md`: the user-facing reference.
- `docs/SECURITY_MODEL.md`: what the exporter promises security-wise, the trust boundaries and how they are checked. `docs/ROADMAP.md`: what is planned and what is deliberately not.
- `docs/grafana/README.md`: dashboard query and panel conventions.
- `internal/captures/README.md`: the capture format, contribution flow and how captures drive the tests.
- `hack/compose/README.md`: the local dev stack, including how to provoke specific alerts.
- `charts/nvidia-gpu-exporter/README.md`: the chart's values reference and deployment notes.
- `.github/CONTRIBUTING.md`: the contributor-facing process.
