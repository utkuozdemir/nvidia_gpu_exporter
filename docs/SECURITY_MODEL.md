# Security model

This page says what you can expect from the exporter security-wise, where the trust boundaries are, and how the project checks that it holds.
For reporting a problem, see [SECURITY.md](../.github/SECURITY.md).

## What the exporter does

The exporter runs `nvidia-smi`, parses the output and serves the result as Prometheus metrics over HTTP.
With the NVML backend it reads the driver library instead.

- It only reads. It never changes GPU settings, and it never writes to disk.
- With the default configuration it makes no outbound connections, the only network activity is answering scrapes. A custom `--nvidia-smi-command` changes that: the documented remote scraping setup runs `ssh`, for example.
- It runs the configured command directly, without a shell. The exporter splits the command line itself, so the flag value cannot smuggle a second command past the configured one. Whoever sets the flag can of course point it at any executable, including a shell or `sudo`. That person controls the configuration, so this is not a boundary.
- When a collection fails, it serves no GPU data instead of stale data. The failure is visible in its own health metrics.

## What you can expect

- The metrics endpoint is plain HTTP by default, like other Prometheus exporters. TLS, client certificates and basic auth can be enabled with `--web.config.file`.
- TLS comes from Go's standard library. The exporter toolkit defaults to TLS 1.2 as the minimum, and the config file can change the minimum version and the cipher suites. The TLS key is its own file, referenced by path. Basic auth passwords are stored as bcrypt hashes in that config file.
- Anyone who can reach the port sees everything the exporter exports. That includes the GPU inventory (UUIDs, serials, PCI addresses, driver and VBIOS versions), and with `--collect.compute-apps`, the names and PIDs of the processes using the GPU. Keep the port on a trusted network, or enable TLS and auth.
- `--web.enable-pprof` additionally serves `/debug/pprof/`, which exposes runtime internals including the full process command line. It is off by default, and it should stay off on an untrusted network.
- A stuck or slow `nvidia-smi` is contained, best-effort. Collections have a timeout that kills the subprocess, scrapes beyond a concurrency limit get a 503, and the health endpoints stay up. The documented limits apply: `--collect.timeout 0` disables the bound, a process stuck in an uninterruptible kernel wait cannot be killed, with a wrapper command only the wrapper itself is signalled, and the command output is buffered in memory without a size bound.
- The NVML backend cannot kill a stuck driver call the way a subprocess can be killed. This is one of the reasons it is experimental.
- The exporter itself needs no privileges. The Linux packages run it as a dedicated system user. Two opt-in setups need more: per-process metrics need host PID sharing to see other workloads' processes, and per-process metrics on MIG GPUs may need a privileged container, see [CONFIGURE.md](CONFIGURE.md#per-process-gpu-metrics).
- The container image runs as uid 65534 (`nobody`), not root. The `USER` directive is numeric because the kubelet cannot verify a `runAsNonRoot` guarantee against a name-based image user. The chart's default pod and container security contexts match, so a default install is admitted in namespaces enforcing the `restricted` Pod Security Standard, see the [chart README](../charts/nvidia-gpu-exporter/README.md).
- The release artifacts are signed keyless with cosign: a signature bundle over the checksums file covers every binary, archive and package, and the container images and the Helm chart are signed by digest. The chart's classic repository additionally carries GPG provenance files, because Helm verifies only those. Release tags are signed, and the release workflow checks that GitHub verifies the tag's signature, that the signed tag names the release and points at the commit being built, and that the commit is on the main branch, before it builds anything. Release tags cannot be moved or deleted. The auto-generated GitHub source archives are not covered. See [Verifying what you downloaded](INSTALL.md#verifying-what-you-downloaded).

## Trust boundaries

1. The network. Clients of the metrics endpoint are untrusted. They can only read.
2. The `nvidia-smi` output. The flags are trusted, since whoever sets them controls the machine anyway. The output is not: it comes from whatever driver, wrapper or remote machine the command runs against. Every parser is fuzzed, and a panic is treated as a bug.
3. The host. The exporter is a normal user process. In a container, the NVIDIA runtime injects the GPU devices and the driver userspace from the host: the driver libraries and utilities, `nvidia-smi` among them. The exporter uses the devices and `nvidia-smi`, or `libnvidia-ml` in nvml mode.
4. The supply chain. Releases are built by the release workflow from a tag. Actions are pinned by commit and base images by digest. The checksums file, the images and the chart are signed with cosign in that workflow, tied to the workflow's identity, and the chart provenance is signed with the project's GPG key.

## Threats and what is done about them

| Threat | What is done |
| --- | --- |
| Command injection via `--nvidia-smi-command` | No shell. The exporter splits the command line and runs the executable directly. |
| Malformed driver output crashes the exporter | Every parser has a fuzz target, and the seed corpora are replayed in every test run. Unknown state values are skipped and logged, never guessed. |
| A stuck `nvidia-smi` exhausts the exporter | The collection timeout kills the subprocess, best-effort as described above. Scrapes beyond the concurrency limit get a 503. Health endpoints do not depend on collection. A wrapper that floods stdout can still grow memory, a known residual risk. |
| Information exposure through the endpoint | Documented above. TLS and auth are available. Per-process metrics and pprof are opt-in. |
| Tampered release artifact | The cosign-signed checksums file covers every built artifact. Images and chart are cosign-signed too, the classic chart repository carries GPG provenance. The install guide has the verification commands. |
| Compromised dependency or action | Renovate updates, Dependabot alerts, actions pinned by commit, images pinned by digest. gosec runs on every change, CodeQL on pull requests and weekly. |
| Endpoint exposed by accident | The default listen address binds all interfaces, like every Prometheus exporter. The install guide covers the Windows firewall rule and the container network settings. |

## Out of scope

- Authorization finer than "can reach the port, or has the password". If different scrapers should see different GPUs, run separate exporters.
- A malicious driver, a malicious `nvidia-smi` binary, or a malicious wrapper command on the host. The exporter trusts what it is configured to execute.
- Confidentiality of the metrics on the wire without TLS configured.

## How this is checked

- The parsers: fuzz targets, run with `task test:fuzz`. The seed corpora are replayed by every `task test` run.
- The end-to-end behavior: the integration suite runs the real exporter against recorded `nvidia-smi` output from every GPU in the corpus and compares the result byte for byte.
- The code: golangci-lint (including gosec) on every pull request and push to the main branch, plus the race detector in the tests. GitHub CodeQL scans through the repository's default setup, on pull requests and weekly.
- The supply chain: OpenSSF Scorecard scores the repository weekly, from the outside. The README badge links the current score.
- The releases: the verification commands in the install guide are run by hand after a release. Nothing automates that yet.
