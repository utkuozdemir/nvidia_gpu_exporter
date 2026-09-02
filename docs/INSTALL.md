# Installation

The sections below are grouped by platform, ordered by how common each
platform is.

| Platform | Ways to install |
| --- | --- |
| [Linux](#linux) | [deb or rpm package](#deb-and-rpm-packages), [binary](#binary), [systemd service without the package](#systemd-service-without-the-package) |
| [Docker](#docker) | [container image](#docker), [docker compose](#docker-compose) |
| [Kubernetes](#kubernetes) | [Helm chart](#helm-chart), [plain DaemonSet](#plain-daemonset) |
| [Windows](#windows) | [all-in-one script](#all-in-one-script), [winget](#winget), [Scoop](#scoop), [Windows service](#running-as-a-windows-service) |
| [macOS](#macos) | [binary](#macos) |

The last section, [Verifying what you downloaded](#verifying-what-you-downloaded),
covers the signatures on the release files, the images and the chart.

## Before you start

The exporter needs the NVIDIA driver on the machine it monitors, and it needs
to be able to run `nvidia-smi`. If `nvidia-smi` prints your GPUs, the
exporter will work.

The exception is the `-nvml` flavor. It reads the driver library directly and
needs no `nvidia-smi`. It exists for Linux x86_64 only, as a separate archive
and as `-nvml` image tags. See [CONFIGURE.md](CONFIGURE.md#experimental-native-nvml-backend)
for what it can and cannot do.

Once installed, the exporter listens on port `9835` and serves the metrics
under `/metrics`. Check it with:

```bash
curl http://localhost:9835/metrics
```

The examples on this page use `latest` and `X.Y.Z` placeholders. Pin a
release version for anything you actually run, so that picking up a new
version stays a deliberate step.

## Linux

Builds exist for x86_64 and arm64. Use whichever of the three ways below
fits your setup:

- the deb or rpm package, if your distro uses systemd. It sets up the
  service for you.
- the plain binary, if you want to run it yourself or your distro is not
  systemd-based.
- the binary plus the systemd unit, if you want the service but not the
  package.

### deb and rpm packages

Download the package for your architecture from the
[releases](https://github.com/utkuozdemir/nvidia_gpu_exporter/releases) and
install it:

```bash
# Debian, Ubuntu and derivatives
sudo dpkg -i nvidia-gpu-exporter_X.Y.Z_linux_amd64.deb

# Fedora, RHEL, Rocky, openSUSE and derivatives
sudo rpm -i nvidia-gpu-exporter_X.Y.Z_linux_amd64.rpm
```

The package puts the binary under `/usr/bin`, creates a system user named
`nvidia_gpu_exporter`, installs the systemd unit and starts the service
enabled on boot. Check it with:

```bash
systemctl status nvidia_gpu_exporter
```

The packages only support systemd. On a distro with another init system, use
the plain binary instead.

### Binary

1. Download the archive for your architecture from the
   [releases](https://github.com/utkuozdemir/nvidia_gpu_exporter/releases).
2. Extract it.
3. Move the binary to somewhere in your `PATH`.

For example, on x86_64:

```bash
# pick a version from https://github.com/utkuozdemir/nvidia_gpu_exporter/releases
VERSION=X.Y.Z
wget https://github.com/utkuozdemir/nvidia_gpu_exporter/releases/download/v${VERSION}/nvidia_gpu_exporter_${VERSION}_linux_x86_64.tar.gz
tar -xvzf nvidia_gpu_exporter_${VERSION}_linux_x86_64.tar.gz
sudo mv nvidia_gpu_exporter /usr/bin
nvidia_gpu_exporter --help
```

The arm64 archive is named `nvidia_gpu_exporter_${VERSION}_linux_arm64.tar.gz`.

The x86_64 releases also ship an `nvidia_gpu_exporter-nvml_*` archive. It is
the same exporter with the experimental NVML backend built in and used by
default, so it needs no `nvidia-smi`. See
[CONFIGURE.md](CONFIGURE.md#experimental-native-nvml-backend) for the details
and the limits.

### Systemd service without the package

These are the steps the deb and rpm packages do for you:

1. Put the binary under `/usr/bin` as described [above](#binary).
2. Create a system user for the service:

   ```bash
   sudo useradd --system --no-create-home --shell /usr/sbin/nologin nvidia_gpu_exporter
   ```

3. Copy [nvidia_gpu_exporter.service](../install/systemd/nvidia_gpu_exporter.service)
   to `/etc/systemd/system/`.
4. Reload systemd and start the service enabled on boot:

   ```bash
   sudo systemctl daemon-reload
   sudo systemctl enable --now nvidia_gpu_exporter
   ```

## Docker

The image does not bundle any NVIDIA components. The
[NVIDIA Container Toolkit](https://docs.nvidia.com/datacenter/cloud-native/container-toolkit/latest/install-guide.html)
injects the GPU devices, the driver libraries and the `nvidia-smi` binary
matching your host driver when the container starts. This way, the same image
works with any driver version, any number of GPUs and any CPU architecture.

You need:

- the NVIDIA driver installed on the host
- the NVIDIA Container Toolkit installed and configured for Docker

Then run:

```bash
docker run -d \
  --name nvidia_gpu_exporter \
  --restart unless-stopped \
  --gpus all \
  -e NVIDIA_DRIVER_CAPABILITIES=utility \
  -p 9835:9835 \
  utkuozdemir/nvidia_gpu_exporter:latest
```

The container runs as uid 65534 (`nobody`), not root. GPU access does not
need root, so nothing else changes because of it.

`--gpus all` turns on Docker's NVIDIA integration for the container and
exposes all GPUs to it.

`NVIDIA_DRIVER_CAPABILITIES=utility` tells the toolkit that the container
only needs the `nvidia-smi` and NVML tier. Recent toolkit versions inject the
full driver userspace either way, so treat the variable as a statement of
intent and as compatibility with older setups, not as a restriction.

The image is `utkuozdemir/nvidia_gpu_exporter` on
[Docker Hub](https://hub.docker.com/r/utkuozdemir/nvidia_gpu_exporter) and
`ghcr.io/utkuozdemir/nvidia_gpu_exporter` on
[GHCR](https://github.com/utkuozdemir/nvidia_gpu_exporter/pkgs/container/nvidia_gpu_exporter),
with the same tags on both.

To try the experimental NVML backend, use a `-nvml` tag, e.g.,
`utkuozdemir/nvidia_gpu_exporter:latest-nvml`. It reads the driver library
directly instead of running `nvidia-smi`. See
[CONFIGURE.md](CONFIGURE.md#experimental-native-nvml-backend).

### Docker compose

```yaml
services:
  nvidia_gpu_exporter:
    image: utkuozdemir/nvidia_gpu_exporter:latest
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

If your setup does not support the device reservation syntax, set
`runtime: nvidia` on the service and add `NVIDIA_VISIBLE_DEVICES=all` to the
environment instead.

> [!IMPORTANT]
> The `NVIDIA_*` environment variables configure the NVIDIA runtime, they do
> not select it. Without `--gpus`, a device reservation or `runtime: nvidia`,
> the container runs on the default runtime and gets no GPU access. The
> exporter still comes up in that case, but it serves only its own health
> metrics, with `nvidia_smi_last_collect_success 0`.

### No shell in the image

The image is distroless: it contains the exporter binary and a minimal
runtime, with no shell, no package manager and no other tools. This keeps it
small and keeps scanner findings near zero.

It also means there is no shell to `docker exec` into, and an image derived
from it cannot run shell commands in `RUN` steps. Running a binary that exists
in the image still works, e.g., `docker exec <container> nvidia-smi` to check
that the driver injection worked.

If you need extra tools next to the exporter, e.g., a wrapper for
`--nvidia-smi-command`, start from a fuller base image and copy the binary in:

```dockerfile
FROM debian:stable-slim
COPY --from=utkuozdemir/nvidia_gpu_exporter:latest /usr/bin/nvidia_gpu_exporter /usr/bin/
ENTRYPOINT ["/usr/bin/nvidia_gpu_exporter"]
```

A wrapper image built this way runs as its own base's default user (root for
`debian`), not as the exporter image's uid 65534, so set up its user, home
and any `sudo` or ssh configuration like in any other image. Only an image
built directly `FROM` the exporter image keeps uid 65534. If you give a
wrapper image a non-root `USER` of its own, keep it numeric: Kubernetes
cannot verify `runAsNonRoot` against a name-based image user and refuses to
start such a container.

When deriving from the `-nvml` variant this way, also carry over its
`NVIDIA_VISIBLE_DEVICES=all` and `NVIDIA_DRIVER_CAPABILITIES=utility`
environment variables. They are what makes the NVIDIA runtime inject the
driver library when the container spec sets no `NVIDIA_*` variables itself.
Without them the exporter fails at startup with a library-not-found error.

### Without the NVIDIA Container Toolkit

On hosts where the toolkit cannot be installed, you can mount the required
pieces into the container yourself:

- each `/dev/nvidia*` device
- the `nvidia-smi` binary
- the `libnvidia-ml.so*` library files from the host library directory

Mount the libraries into the container's default library directory
(`/usr/lib/x86_64-linux-gnu` on amd64, `/usr/lib/aarch64-linux-gnu` on
arm64), because the image carries no `ldconfig` to register any other
location. Alternatively, set `LD_LIBRARY_PATH` to the directory you chose.

This is fragile: the library symlink chain breaks on driver upgrades, the
device list varies with the GPU count, and library paths differ per
distribution and architecture. Prefer the toolkit whenever possible.

Whichever mechanism injects the devices, the container's uid 65534 must be
able to read them. The driver creates `/dev/nvidia*` world-accessible by
default, so this just works. A host that restricts them (the
`NVreg_DeviceFileMode` module parameter, a udev rule) shows up as
`nvidia_smi_last_collect_success 0`: relax the device mode, add the owning
group with `--group-add`, or run the container with `--user 0`.

## Kubernetes

The exporter runs as a DaemonSet with the NVIDIA runtime. The runtime then
injects GPU access on each node, the same way it does for Docker above.

> [!IMPORTANT]
> Do **not** request an `nvidia.com/gpu` resource for the exporter. The
> device plugin allocates whole GPUs exclusively, so a monitoring pod that
> requests one takes that GPU away from real workloads. The environment
> variable approach below gives the exporter visibility of all GPUs on the
> node without reserving any of them.

The nodes need the NVIDIA driver and the NVIDIA Container Toolkit configured
for the container runtime. Managed GPU node images (AKS, GKE, EKS) typically
ship both. On self-managed nodes with containerd (including k3s), install the
toolkit and either make the NVIDIA runtime the default or create a
`RuntimeClass` named `nvidia` and reference it as below.

### Helm chart

The [Helm chart](../charts/nvidia-gpu-exporter) lives in this repository and
implements all of the above:

```bash
helm install nvidia-gpu-exporter oci://ghcr.io/utkuozdemir/charts/nvidia-gpu-exporter \
  --set runtimeClassName=nvidia
```

See the [chart README](../charts/nvidia-gpu-exporter/README.md) for the full
values reference, the optional monitoring extras (ServiceMonitor, PodMonitor,
alerts, the Grafana dashboards), and the migration notes if you are coming
from the old chart repository.

### Plain DaemonSet

If you prefer not to use Helm, a minimal DaemonSet. In a namespace enforcing
the `restricted` Pod Security Standard, also add the security contexts the
chart sets by default (see the
[chart README](../charts/nvidia-gpu-exporter/README.md#restricted-namespaces)):

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
      runtimeClassName: nvidia # omit if the NVIDIA runtime is your cluster default
      containers:
        - name: exporter
          image: utkuozdemir/nvidia_gpu_exporter:latest # pin a release tag in production
          env:
            - name: NVIDIA_VISIBLE_DEVICES
              value: all
            - name: NVIDIA_DRIVER_CAPABILITIES
              value: utility
          ports:
            - containerPort: 9835
              name: metrics
```

### Managed Kubernetes (AKS and similar)

GPU node images on managed Kubernetes ship the NVIDIA driver and the
container toolkit as part of the node image, so the DaemonSet above works
as-is.

The `nvidia.com/gpu` resource and the NVIDIA device plugin exist for
scheduling GPU workloads. The exporter deliberately stays out of that
mechanism, since it only needs to observe the GPUs. Allocation limits such as
whole-number allocations and one workload per GPU therefore apply to your
workloads, but never to the exporter.

## Windows

If you do not have Prometheus and Grafana yet, the all-in-one script below
installs all three on the same machine. If you already have them, install the
exporter alone with winget or Scoop and register it as a service.

Only an x86_64 build exists for Windows.

### All-in-one script

The PowerShell script installs the exporter, Prometheus and Grafana on the
same machine, with the datasource and the dashboards already provisioned.

1. Download [the installation script](https://raw.githubusercontent.com/utkuozdemir/nvidia_gpu_exporter/main/install/windows-all-in-one.ps1)
   and save it with the `.ps1` extension.
2. Open an administrative PowerShell prompt (search for PowerShell in the
   start menu, right-click, Run as administrator).
3. Run the script with the execution policy bypassed for this one run, since
   a downloaded script is otherwise blocked by the default policy:

   ```PowerShell
   powershell -NoProfile -ExecutionPolicy Bypass -File "C:\Users\<YOUR_USERNAME>\Downloads\windows-all-in-one.ps1"
   ```

4. Open [http://localhost:9090](http://localhost:9090) to check that
   Prometheus is running.
5. Open [http://localhost:3000](http://localhost:3000) to check that Grafana
   is running.
6. Log in to Grafana with the initial credentials `admin` / `admin` and set a
   new password if you like.
7. The Prometheus datasource and the "Nvidia GPU Metrics" and "Nvidia GPU
   Overview" dashboards are already there. Open them from the dashboards
   list.

Re-running the script is safe. It updates the exporter to the latest release
and leaves the Prometheus data and the Grafana state in place.

> [!NOTE]
> If you installed with an earlier version of this script (based on Scoop and
> NSSM), or want to remove everything the script installed, run
> [the uninstall script](https://raw.githubusercontent.com/utkuozdemir/nvidia_gpu_exporter/main/install/windows-all-in-one-uninstall.ps1)
> the same way as the install script. It removes the services and the program
> files but never deletes collected data, and it prints the kept locations.
> A new installation starts with a fresh Prometheus database, the data from an
> old Scoop-based setup is not carried over.

### winget

```PowerShell
winget install utkuozdemir.nvidia_gpu_exporter
```

That puts `nvidia_gpu_exporter` on your `PATH`, so you can run it directly.

To run it as a service instead, use the machine-wide install (not the per-user
one above), so that the service account can reach the binary and it keeps
working across upgrades. Then register the service:

```PowerShell
winget install --scope machine utkuozdemir.nvidia_gpu_exporter
nvidia_gpu_exporter install
```

Use one or the other, not both. See
[Running as a Windows service](#running-as-a-windows-service) for what
`install` does and how to manage the service.

### Scoop

If you don't have [Scoop](https://scoop.sh) yet, open a regular PowerShell
prompt and install it:

```PowerShell
Set-ExecutionPolicy RemoteSigned -Scope CurrentUser
Invoke-Expression (New-Object System.Net.WebClient).DownloadString('https://get.scoop.sh')
```

Then install the exporter from its bucket. The `--global` scope keeps the
path stable across updates, which matters when it runs as a service:

```PowerShell
scoop install git
scoop bucket add nvidia_gpu_exporter https://github.com/utkuozdemir/scoop_nvidia_gpu_exporter.git
scoop install nvidia_gpu_exporter/nvidia_gpu_exporter --global
```

The binary ends up at
`C:\ProgramData\scoop\apps\nvidia_gpu_exporter\current\nvidia_gpu_exporter.exe`.

### Running as a Windows service

The exporter speaks the Windows service control manager protocol itself, so it
runs as a normal Windows service registered with its own `install` command.
No third-party service wrapper such as NSSM is needed.

Get the binary with [winget](#winget) or [Scoop](#scoop), or download the
Windows archive from the
[releases](https://github.com/utkuozdemir/nvidia_gpu_exporter/releases) and
extract `nvidia_gpu_exporter.exe` somewhere stable, e.g.,
`C:\Program Files\nvidia_gpu_exporter\`.

Then, from a PowerShell prompt opened as administrator:

```PowerShell
# Register the service. It starts automatically on boot and restarts on failure.
# Use the full path if the binary is not on your PATH, e.g., the Scoop location:
# & 'C:\ProgramData\scoop\apps\nvidia_gpu_exporter\current\nvidia_gpu_exporter.exe' install
nvidia_gpu_exporter install

# Allow the metrics port through the firewall, scoped to the local network.
# Only needed if Prometheus scrapes from another machine. If Prometheus runs
# on this same box, loopback is never firewalled and you can skip this.
# Drop -RemoteAddress (or widen it) if your Prometheus is on another subnet.
New-NetFirewallRule -DisplayName "Nvidia GPU Exporter" -Direction Inbound -Action Allow -Protocol TCP -LocalPort 9835 -Profile Private,Domain -RemoteAddress LocalSubnet

# Start it.
Start-Service nvidia_gpu_exporter
```

Any flags you pass to `install` are baked into the service command line, so
you configure the service exactly like you would when running it
interactively. With no flags it listens on `:9835`. For example, to use a
different port:

```PowerShell
nvidia_gpu_exporter install --web.listen-address=:9836
```

Running `install` again reconfigures the existing service in place. To change
the flags later, re-run it with the new ones, no need to uninstall first.
Restart the service afterwards for the new command line to take effect:

```PowerShell
Restart-Service nvidia_gpu_exporter
```

The service writes its logs to the Windows Event Log, into the `Application`
log under the source `nvidia_gpu_exporter`.

Manage and remove the service with the usual tooling:

```PowerShell
# Status, stop, start
Get-Service nvidia_gpu_exporter
Stop-Service nvidia_gpu_exporter
Start-Service nvidia_gpu_exporter

# Uninstall the service (stop it first). Use the full path if the binary is
# not on your PATH, e.g., .\nvidia_gpu_exporter.exe uninstall
Stop-Service nvidia_gpu_exporter
nvidia_gpu_exporter uninstall

# Remove the firewall rule if you added one.
Remove-NetFirewallRule -DisplayName "Nvidia GPU Exporter"
```

Afterwards, remove the binary with
`winget uninstall utkuozdemir.nvidia_gpu_exporter` or
`scoop uninstall nvidia_gpu_exporter --global` (the same `--global` scope it
was installed with).

> [!NOTE]
> Earlier versions of this guide used [NSSM](https://nssm.cc) to run the
> exporter as a service. If you installed it that way, remove the old service
> first from an administrator PowerShell prompt, then follow the steps above:
>
> ```PowerShell
> Stop-Service nvidia_gpu_exporter
> nssm remove nvidia_gpu_exporter confirm
> ```
>
> NSSM is no longer needed. If nothing else on the machine uses it, you can
> drop it too with `scoop uninstall nssm`.

## macOS

Download the `darwin_x86_64` archive from the
[releases](https://github.com/utkuozdemir/nvidia_gpu_exporter/releases),
extract it and move the binary to somewhere in your `PATH`. There is only an
Intel build, which also runs on Apple Silicon through Rosetta.

A Mac usually has no NVIDIA GPU of its own, so the typical setup on macOS is
remote scraping: the exporter runs on the Mac and runs `nvidia-smi` on another
machine over SSH. See [CONFIGURE.md](CONFIGURE.md#remote-scraping) for how to
set that up.

## Verifying what you downloaded

Everything the release pipeline publishes is signed, so you can check that a
file or an image really came from this project's release workflow. The
signatures are keyless (Sigstore): they are tied to the identity of the
release workflow, which is what the commands below check. The Helm chart's
classic repository is the exception, since Helm verifies GPG provenance only.

### Release archives and packages

Each release ships a `checksums.txt` with the sha256 sums of every binary,
archive and package, and a signature bundle `checksums.txt.sigstore.json` that
covers it. Check the signature with cosign v3 or newer, then the checksum of
what you downloaded:

```bash
cosign verify-blob checksums.txt \
  --bundle checksums.txt.sigstore.json \
  --certificate-oidc-issuer=https://token.actions.githubusercontent.com \
  --certificate-identity-regexp='^https://github\.com/utkuozdemir/nvidia_gpu_exporter/\.github/workflows/release\.yml@refs/tags/v.*$' &&
sha256sum --ignore-missing -c checksums.txt
```

Releases with no `checksums.txt.sigstore.json` attached shipped a detached GPG
signature `checksums.txt.asc` instead, made with the key that still signs the
chart provenance. For those, import the key and check the two files. The
earliest releases have neither file, only the checksums.

```bash
curl -fsSL https://utkuozdemir.github.io/nvidia_gpu_exporter/pubkey.asc | gpg --import &&
gpg --verify checksums.txt.asc checksums.txt &&
sha256sum --ignore-missing -c checksums.txt
```

### Container images

The images on Docker Hub and GHCR are signed keyless with cosign. Verify one
like this, replacing `VERSION` with a tag released after signing was
introduced:

```bash
cosign verify \
  --certificate-oidc-issuer=https://token.actions.githubusercontent.com \
  --certificate-identity-regexp='^https://github\.com/utkuozdemir/nvidia_gpu_exporter/\.github/workflows/release\.yml@refs/tags/v.*$' \
  utkuozdemir/nvidia_gpu_exporter:VERSION
```

### Chart

The chart is signed too, with GPG provenance for the classic repository and
cosign for the OCI artifact. The commands are in the
[chart README](../charts/nvidia-gpu-exporter/README.md).

### Build provenance

Every archive and package listed in `checksums.txt`, every image and the chart
also carry a build provenance attestation. It is a signed statement, made by
GitHub during the release workflow, that ties the artifact's digest to this
repository, the exact commit, the workflow and the run that produced it. It
answers a different question than the signatures above: not only "is this the
maintainer's release", but "was this built by the release workflow from that
commit".

Verify a downloaded file with the GitHub CLI. The two extra flags pin the
attestation to the release workflow and to the release tag, so an attestation
made by anything else in the repository is rejected:

```bash
gh attestation verify nvidia_gpu_exporter_X.Y.Z_linux_x86_64.tar.gz \
  --repo utkuozdemir/nvidia_gpu_exporter \
  --signer-workflow github.com/utkuozdemir/nvidia_gpu_exporter/.github/workflows/release.yml \
  --source-ref refs/tags/vX.Y.Z
```

Images and the chart are verified by reference. The chart version is not the
app version: its major is the app major plus one, so app `1.14.0` has chart
`2.14.0`.

```bash
gh attestation verify oci://docker.io/utkuozdemir/nvidia_gpu_exporter:X.Y.Z \
  --repo utkuozdemir/nvidia_gpu_exporter \
  --signer-workflow github.com/utkuozdemir/nvidia_gpu_exporter/.github/workflows/release.yml \
  --source-ref refs/tags/vX.Y.Z
gh attestation verify oci://ghcr.io/utkuozdemir/charts/nvidia-gpu-exporter:CHART_VERSION \
  --repo utkuozdemir/nvidia_gpu_exporter \
  --signer-workflow github.com/utkuozdemir/nvidia_gpu_exporter/.github/workflows/release.yml \
  --source-ref refs/tags/vX.Y.Z
```

The output names the commit and the workflow run, so you can follow it back
to the source and the build log. `checksums.txt` itself is not attested, it
is covered by its signature bundle.

### Reproducing a release

From v1.15.1 on, the release files are built reproducibly. The same commit
gives the same bytes for every binary, archive and package, on any machine.

One exception: the nvml flavor is a cgo build. Its binary carries the C
compiler of the builder, so it reproduces only on a matching toolchain. The
release runner is Ubuntu 24.04 with its default GCC.

The release workflow rebuilds every release on a second runner, from scratch,
and fails if a checksum differs. You can do the same:

1. Clone the repository and check out the release tag.
2. Install the Go version `go.mod` declares and the goreleaser version
   `hack/dev.Dockerfile` pins (the `GORELEASER_VERSION` line).
3. Build the release files without publishing anything, and compare:

```bash
PRIVATE_ACCESS_TOKEN=placeholder goreleaser release --clean --skip=publish,sign,sbom,docker,announce
diff dist/checksums.txt <(curl -fsSL https://github.com/utkuozdemir/nvidia_gpu_exporter/releases/download/vX.Y.Z/checksums.txt)
```

An empty diff means every file you built is byte for byte the one that was
published. On a machine with another C toolchain, the nvml archive is the one
line that differs.

The container images are reproducible too. Comparing them needs a push to a
registry, see `AGENTS.md`.
