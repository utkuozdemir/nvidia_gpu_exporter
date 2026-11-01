# Installation

| Platform | Ways to install |
| --- | --- |
| [Linux](#linux) | [deb or rpm package](#deb-and-rpm-packages), [binary](#binary), [systemd service without the package](#systemd-service-without-the-package) |
| [Docker](#docker) | [container image](#docker), [docker compose](#docker-compose) |
| [Kubernetes](#kubernetes) | [Helm chart](#helm-chart), [plain DaemonSet](#plain-daemonset) |
| [Windows](#windows) | [all-in-one script](#all-in-one-script), [winget](#winget), [Scoop](#scoop), [Windows service](#running-as-a-windows-service) |
| [macOS](#macos) | [binary](#macos) |

The last section, [Verifying what you downloaded](#verifying-what-you-downloaded), covers signatures and build provenance for the release files, images and Helm chart.

## Before you start

The exporter requires the NVIDIA driver on the monitored machine. We recommend the native `nvml` backend (`nvidia_gpu_exporter-nvml_*` archives and `:X.Y.Z-nvml` container images on Linux, or `--collect.backend=nvml` on Windows), while the plain (non-`-nvml`) archives, `deb`/`rpm` packages, and `:X.Y.Z` images default to the `exec` (`nvidia-smi` subprocess) backend for 1.x compatibility (see [CONFIGURE.md](CONFIGURE.md#native-nvml-backend)).

Once running, the exporter listens on port `9835` and serves metrics at `/metrics`:

```bash
curl http://localhost:9835/metrics
```

The examples below use `latest` and `X.Y.Z` placeholders. Pin a specific release version in production.

## Linux

Builds are published for `x86_64` and `arm64`:

- **Binary archive**: available in both the `-nvml` flavor (`nvidia_gpu_exporter-nvml_*`, recommended on glibc systems) and the static `exec` flavor (`nvidia_gpu_exporter_*`).
- **`deb` or `rpm` package**: ships the static `exec` binary and sets up the `systemd` unit automatically.

### deb and rpm packages

Download the package for your architecture from the [releases page](https://github.com/utkuozdemir/nvidia_gpu_exporter/releases) and install it:

```bash
# Debian, Ubuntu and derivatives
sudo dpkg -i nvidia-gpu-exporter_X.Y.Z_linux_amd64.deb

# Fedora, RHEL, Rocky, openSUSE and derivatives
sudo rpm -i nvidia-gpu-exporter_X.Y.Z_linux_amd64.rpm
```

The package installs `/usr/bin/nvidia_gpu_exporter`, creates a `nvidia_gpu_exporter` system user, and enables and starts the `systemd` service:

```bash
systemctl status nvidia_gpu_exporter
```

### Binary

Download either the `-nvml` archive (recommended, defaults to `--collect.backend=nvml` on glibc systems) or the static archive from the [releases page](https://github.com/utkuozdemir/nvidia_gpu_exporter/releases) and move `nvidia_gpu_exporter` into `/usr/bin`:

```bash
VERSION=X.Y.Z
# NVML flavor (x86_64, use linux_arm64 on ARM):
wget https://github.com/utkuozdemir/nvidia_gpu_exporter/releases/download/v${VERSION}/nvidia_gpu_exporter-nvml_${VERSION}_linux_x86_64.tar.gz
tar -xvzf nvidia_gpu_exporter-nvml_${VERSION}_linux_x86_64.tar.gz
sudo mv nvidia_gpu_exporter /usr/bin
nvidia_gpu_exporter --help
```

For the static `exec` build, omit `-nvml` from the archive name (`nvidia_gpu_exporter_${VERSION}_linux_x86_64.tar.gz`).

### Systemd service without the package

To run the downloaded binary (such as the `-nvml` build) as a `systemd` service:

1. Place `/usr/bin/nvidia_gpu_exporter` as shown [above](#binary).
2. Create the system user:

   ```bash
   sudo useradd --system --no-create-home --shell /usr/sbin/nologin nvidia_gpu_exporter
   ```

3. Copy [nvidia_gpu_exporter.service](../install/systemd/nvidia_gpu_exporter.service) to `/etc/systemd/system/`.
4. Reload `systemd` and start the service:

   ```bash
   sudo systemctl daemon-reload
   sudo systemctl enable --now nvidia_gpu_exporter
   ```

### Systemd unit hardening

The [systemd unit](../install/systemd/nvidia_gpu_exporter.service) runs the exporter with this sandboxing, which still lets `sudo` and `ssh` wrapper commands work:

- `PrivateTmp=yes`
- `ProtectSystem=full`
- `ProtectHome=read-only`
- `ProtectControlGroups=yes`

A wrapper that writes to the home directory (e.g., `~/.ssh/known_hosts`) or uses an agent socket in `/tmp` needs `ProtectHome=no` or `PrivateTmp=no`. Override them with `sudo systemctl edit nvidia_gpu_exporter`.

Hosts that match all of the following can use stricter sandboxing:

- The exporter collects without a `sudo` or `ssh` wrapper command.
- No configuration is stored under `/home`, `/root` or `/run/user`.
- The `/dev/nvidia*` device nodes are created at boot (by `nvidia-persistenced` or `udev` rules), not on demand by `nvidia-modprobe`.

Add it with `sudo systemctl edit nvidia_gpu_exporter`:

```ini
[Service]
NoNewPrivileges=yes
ProtectSystem=strict
ProtectHome=yes
ProtectKernelTunables=yes
ProtectKernelModules=yes
RestrictSUIDSGID=yes
LockPersonality=yes
```

## Docker

The container image does not bundle NVIDIA driver files. Instead, the [NVIDIA Container Toolkit](https://docs.nvidia.com/datacenter/cloud-native/container-toolkit/latest/install-guide.html) injects the host's GPU device nodes, driver libraries and `nvidia-smi` when the container starts.

With the NVIDIA driver and the NVIDIA Container Toolkit installed on the host, run the recommended `-nvml` image tag:

```bash
docker run -d \
  --name nvidia_gpu_exporter \
  --restart unless-stopped \
  --gpus all \
  -e NVIDIA_DRIVER_CAPABILITIES=utility \
  -p 9835:9835 \
  utkuozdemir/nvidia_gpu_exporter:latest-nvml
```

Use `utkuozdemir/nvidia_gpu_exporter:latest` instead for the static `exec` backend.

Images are published to [Docker Hub](https://hub.docker.com/r/utkuozdemir/nvidia_gpu_exporter) (`utkuozdemir/nvidia_gpu_exporter`) and [GHCR](https://github.com/utkuozdemir/nvidia_gpu_exporter/pkgs/container/nvidia_gpu_exporter) (`ghcr.io/utkuozdemir/nvidia_gpu_exporter`), and run as uid `65534` (`nobody`).

### Docker compose

```yaml
services:
  nvidia_gpu_exporter:
    image: utkuozdemir/nvidia_gpu_exporter:latest-nvml
    restart: unless-stopped
    environment:
      - NVIDIA_DRIVER_CAPABILITIES=utility
    ports:
      - "9835:9835"
    deploy:
      resources:
        reservations:
          devices:
            - driver: nvidia
              count: all
              capabilities: [gpu]
```

If your Compose version does not support device reservations, set `runtime: nvidia` on the service and add `NVIDIA_VISIBLE_DEVICES=all` to `environment`.

> [!IMPORTANT]
> The `NVIDIA_*` environment variables configure the NVIDIA runtime, they do not select it. Without `--gpus`, a device reservation or `runtime: nvidia`, the container runs on the default runtime without GPU access: the default `exec` image comes up and reports `nvidia_smi_last_collect_success 0`, while the `-nvml` image exits at startup because `libnvidia-ml.so.1` is not injected.

### No shell in the image

The image is distroless and contains only the exporter binary and minimal runtime files, with no shell or package manager.

If you need extra tools alongside the exporter (such as `ssh` or `sudo` for `--nvidia-smi-command`), copy the binary into a fuller base image:

```dockerfile
FROM debian:stable-slim
COPY --from=utkuozdemir/nvidia_gpu_exporter:latest /usr/bin/nvidia_gpu_exporter /usr/bin/
ENTRYPOINT ["/usr/bin/nvidia_gpu_exporter"]
```

When deriving from a fuller base image, configure a numeric non-root `USER` if your Kubernetes cluster enforces `runAsNonRoot`. When copying the `-nvml` binary, also set `NVIDIA_VISIBLE_DEVICES=all` and `NVIDIA_DRIVER_CAPABILITIES=utility` so the container runtime injects `libnvidia-ml.so.1`.

### Without the NVIDIA Container Toolkit

On hosts where the toolkit cannot be installed, mount `/dev/nvidia*`, `libnvidia-ml.so*` (into `/usr/lib/x86_64-linux-gnu` on amd64 or `/usr/lib/aarch64-linux-gnu` on arm64, or set `LD_LIBRARY_PATH`), and `nvidia-smi` (when using `exec`) directly into the container. Prefer the NVIDIA Container Toolkit whenever possible, since manual library mounts break across driver upgrades.

If the host restricts `/dev/nvidia*` permissions (for example via `NVreg_DeviceFileMode`), grant the container access with `--group-add` or `--user 0`.

## Kubernetes

The exporter runs as a DaemonSet with the NVIDIA container runtime injecting GPU access on each node (nodes need the NVIDIA driver, the [NVIDIA Container Toolkit](https://docs.nvidia.com/datacenter/cloud-native/container-toolkit/latest/install-guide.html), and an `nvidia` `RuntimeClass` unless the NVIDIA runtime is the node default or Dynamic Resource Allocation is used).

> [!IMPORTANT]
> Do **not** request an `nvidia.com/gpu` resource for the exporter. The device plugin allocates whole GPUs exclusively, so requesting one would take a GPU away from workloads.

### Helm chart

Install the [Helm chart](../charts/nvidia-gpu-exporter) (pass `--set image.tag=X.Y.Z-nvml` to run the recommended NVML image variant):

```bash
helm install nvidia-gpu-exporter oci://ghcr.io/utkuozdemir/charts/nvidia-gpu-exporter \
  --set runtimeClassName=nvidia
```

See the [chart README](../charts/nvidia-gpu-exporter/README.md) for the values reference, ServiceMonitor, PrometheusRule alerts, Grafana dashboards, MIG configuration and Dynamic Resource Allocation (DRA).

### Plain DaemonSet

If you do not use Helm, deploy a DaemonSet directly (in a namespace enforcing the `restricted` Pod Security Standard, include the security contexts from the [chart README](../charts/nvidia-gpu-exporter/README.md)):

```yaml
apiVersion: apps/v1
kind: DaemonSet
metadata:
  name: nvidia-gpu-exporter
spec:
  selector:
    matchLabels:
      app: nvidia-gpu-exporter
  template:
    metadata:
      labels:
        app: nvidia-gpu-exporter
    spec:
      runtimeClassName: nvidia # omit if the NVIDIA runtime is the cluster default
      containers:
        - name: exporter
          image: utkuozdemir/nvidia_gpu_exporter:latest-nvml # pin a release tag in production
          env:
            - name: NVIDIA_VISIBLE_DEVICES
              value: all
            - name: NVIDIA_DRIVER_CAPABILITIES
              value: utility
          ports:
            - containerPort: 9835
              name: metrics
```

### Dynamic Resource Allocation (DRA)

On Kubernetes 1.34 or newer using the [NVIDIA DRA driver](https://github.com/kubernetes-sigs/dra-driver-nvidia-gpu) (`dra-driver-nvidia-gpu`) without an `nvidia` `RuntimeClass`, leave `runtimeClassName` empty, label the namespace with `resource.kubernetes.io/admin-access: "true"`, and use a `ResourceClaimTemplate` with `allocationMode: All` and `adminAccess: true`. See the [chart README](../charts/nvidia-gpu-exporter/README.md#dynamic-resource-allocation-dra) for the full manifest and values.

## Windows

Only an `x86_64` build is published for Windows, and it includes both the `nvml` and `exec` backends in a single `nvidia_gpu_exporter.exe` binary.

### All-in-one script

If you do not have Prometheus and Grafana installed yet, the PowerShell script installs the exporter, Prometheus and Grafana on the same machine with the datasource and dashboards pre-provisioned:

1. Download [windows-all-in-one.ps1](https://raw.githubusercontent.com/utkuozdemir/nvidia_gpu_exporter/main/install/windows-all-in-one.ps1).
2. Open an elevated PowerShell prompt (Run as administrator) and execute:

   ```PowerShell
   powershell -NoProfile -ExecutionPolicy Bypass -File "C:\Users\<YOUR_USERNAME>\Downloads\windows-all-in-one.ps1"
   ```

3. Open [http://localhost:9090](http://localhost:9090) for Prometheus and [http://localhost:3000](http://localhost:3000) for Grafana (initial login `admin` / `admin`).

Re-running the script updates the exporter in place while keeping existing Prometheus data and Grafana state. The script registers the service with the default flags (`exec` backend), and re-running it resets the service flags. To switch the service to the recommended `nvml` backend afterwards, run `& "$env:ProgramFiles\nvidia_gpu_exporter\nvidia_gpu_exporter.exe" install --collect.backend=nvml` and `Restart-Service nvidia_gpu_exporter` from an elevated PowerShell prompt. To uninstall the stack while preserving collected data, run [windows-all-in-one-uninstall.ps1](https://raw.githubusercontent.com/utkuozdemir/nvidia_gpu_exporter/main/install/windows-all-in-one-uninstall.ps1).

### winget

To install for interactive use:

```PowerShell
winget install utkuozdemir.nvidia_gpu_exporter
```

To run as a Windows service, install machine-wide so the service account can access the binary across upgrades, and register the service (passing `--collect.backend=nvml` to use the NVML backend):

```PowerShell
winget install --scope machine utkuozdemir.nvidia_gpu_exporter
nvidia_gpu_exporter install --collect.backend=nvml
```

### Scoop

Install Scoop if needed, then install the exporter globally so the binary path stays stable across updates:

```PowerShell
scoop install git
scoop bucket add nvidia_gpu_exporter https://github.com/utkuozdemir/scoop_nvidia_gpu_exporter.git
scoop install nvidia_gpu_exporter/nvidia_gpu_exporter --global
```

The binary is installed at `C:\ProgramData\scoop\apps\nvidia_gpu_exporter\current\nvidia_gpu_exporter.exe`.

### Running as a Windows service

`nvidia_gpu_exporter.exe` implements the Windows service control manager protocol directly, with no external service wrapper required.

From an elevated PowerShell prompt:

```PowerShell
# Register the service with the NVML backend (omit --collect.backend=nvml for the nvidia-smi backend)
nvidia_gpu_exporter install --collect.backend=nvml

# Allow port 9835 from the local subnet if Prometheus scrapes from another machine
New-NetFirewallRule -DisplayName "Nvidia GPU Exporter" -Direction Inbound -Action Allow -Protocol TCP -LocalPort 9835 -Profile Private,Domain -RemoteAddress LocalSubnet

# Start the service
Start-Service nvidia_gpu_exporter
```

Any flags passed to `install` are saved into the service command line. Re-run `nvidia_gpu_exporter install <flags>` and `Restart-Service nvidia_gpu_exporter` to update flags in place. Logs are written to the Windows Application Event Log under source `nvidia_gpu_exporter`.

Manage or remove the service with PowerShell:

```PowerShell
Get-Service nvidia_gpu_exporter
Stop-Service nvidia_gpu_exporter
nvidia_gpu_exporter uninstall
Remove-NetFirewallRule -DisplayName "Nvidia GPU Exporter"
```

## macOS

Download the `darwin_x86_64` archive from the [releases page](https://github.com/utkuozdemir/nvidia_gpu_exporter/releases) (runs on Apple Silicon through Rosetta). Because macOS hosts do not have local NVIDIA drivers, macOS builds are used for remote scraping over SSH (`--nvidia-smi-command`) or `--collect.backend=demo`. See [CONFIGURE.md](CONFIGURE.md#remote-scraping).

## Verifying what you downloaded

From `v1.15.0` on, `checksums.txt` is signed keyless with Sigstore (`cosign`) and every release archive, package, container image, and OCI Helm chart carries a GitHub build provenance attestation. Container images and the OCI Helm chart are cosign-signed from `v1.10.0` on, and `checksums.txt` in `v1.10.0` to `v1.14.0` plus the classic Helm repository are signed with GPG key [`93122B2C53431C2F60964EB7EAC49314A32B9205`](https://utkuozdemir.github.io/nvidia_gpu_exporter/pubkey.asc) (releases before `v1.10.0` are unsigned).

### Release archives and packages

Verify `checksums.txt` against `checksums.txt.sigstore.json` with `cosign` v3+, then check the archive checksum:

```bash
cosign verify-blob checksums.txt \
  --bundle checksums.txt.sigstore.json \
  --certificate-oidc-issuer=https://token.actions.githubusercontent.com \
  --certificate-identity-regexp='^https://github\.com/utkuozdemir/nvidia_gpu_exporter/\.github/workflows/release\.yml@refs/tags/v.*$' &&
sha256sum --ignore-missing -c checksums.txt
```

On macOS, replace `sha256sum` with `shasum -a 256`. For releases `v1.10.0` to `v1.14.0`, verify `checksums.txt.asc` with GPG instead (`curl -fsSL https://utkuozdemir.github.io/nvidia_gpu_exporter/pubkey.asc | gpg --import && gpg --verify checksums.txt.asc checksums.txt`).

### Container images and Helm chart

Verify a container image tag (`X.Y.Z` or `X.Y.Z-nvml`) with `cosign`:

```bash
cosign verify \
  --certificate-oidc-issuer=https://token.actions.githubusercontent.com \
  --certificate-identity-regexp='^https://github\.com/utkuozdemir/nvidia_gpu_exporter/\.github/workflows/release\.yml@refs/tags/v.*$' \
  utkuozdemir/nvidia_gpu_exporter:VERSION
```

Helm chart verification commands (cosign for OCI and GPG provenance for the classic repository) are in the [chart README](../charts/nvidia-gpu-exporter/README.md#verifying-the-chart-signature).

### Build provenance

Verify build provenance attestations with the GitHub CLI (`gh`):

```bash
gh attestation verify nvidia_gpu_exporter_X.Y.Z_linux_x86_64.tar.gz \
  --repo utkuozdemir/nvidia_gpu_exporter \
  --signer-workflow github.com/utkuozdemir/nvidia_gpu_exporter/.github/workflows/release.yml \
  --source-ref refs/tags/vX.Y.Z

gh attestation verify oci://docker.io/utkuozdemir/nvidia_gpu_exporter:X.Y.Z \
  --repo utkuozdemir/nvidia_gpu_exporter \
  --signer-workflow github.com/utkuozdemir/nvidia_gpu_exporter/.github/workflows/release.yml \
  --source-ref refs/tags/vX.Y.Z
```

From `v1.17.0` on, GitHub releases are immutable: once a release is published, its assets and its tag cannot be changed. GitHub also attests each release, and a downloaded file can be checked against that attestation:

```bash
gh release verify-asset vX.Y.Z nvidia_gpu_exporter_X.Y.Z_linux_x86_64.tar.gz \
  --repo utkuozdemir/nvidia_gpu_exporter
```

### Reproducing a release

From `v1.15.1` on, static release artifacts are reproducible across machines from the tagged commit. The `-nvml` cgo archive reproduces on builders with the same C toolchain (the default GCC on the Ubuntu runner image in `.github/workflows/release.yml` for that tag).

To rebuild locally and compare against the published `checksums.txt`, check out the release tag and build with the Go version in `go.mod` and the `GORELEASER_VERSION` pinned in `hack/dev.Dockerfile`:

```bash
PRIVATE_ACCESS_TOKEN=placeholder goreleaser release --clean --skip=publish,sign,sbom,docker,announce
diff dist/checksums.txt <(curl -fsSL https://github.com/utkuozdemir/nvidia_gpu_exporter/releases/download/vX.Y.Z/checksums.txt)
```
