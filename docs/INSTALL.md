# Installation

There are many installation methods for different use cases.

## All-in-One Windows Installation

If you use Windows and not familiar with the tools like Prometheus/Grafana,
you can simply use the PowerShell installation script to get the Exporter,
Prometheus and Grafana installed on the same machine.

Follow the steps below:

1. Download [the installation script](https://raw.githubusercontent.com/utkuozdemir/nvidia_gpu_exporter/main/install/windows.ps1) (save it with `.ps1` extension)
2. Open an administrative PowerShell prompt (search for PowerShell in the start menu - right-click - Run as Administrator)
3. In the prompt, execute the script you have downloaded. For example, `C:\Users\<YOUR_USERNAME>\Downloads\windows.ps1`
4. Verify that you have Prometheus running by opening [http://localhost:9090](http://localhost:9090) in your browser.
5. Verify that you have Grafana running by opening [http://localhost:3000](http://localhost:3000) in your browser.
6. Login to Grafana using the initial credentials: `admin` - `admin`. Set a new password if you like.
7. On Grafana, choose the option "Create - Import" from the top-left (big plus sign).
8. Enter `14574` to the ID field and click "Load".
9. Hit "Import". The dashboard picks your Prometheus data source automatically.
   If you have more than one, use the "Data source" dropdown at the top of the
   dashboard.
10. Enjoy the dashboard!

## Using .deb or .rpm packages

If you are on a Debian-based system (.deb), you can install the exporter with the following command:

```bash
sudo dpkg -i nvidia-gpu-exporter_1.3.1_linux_amd64.deb
```

If you are on a Red Hat-based system (.rpm), you can install the exporter with the following command:

```bash
sudo rpm -i nvidia-gpu-exporter_1.3.1_linux_amd64.rpm
```

**Note:** .rpm and .deb packages only support systems using systemd as init system.

## By downloading the binaries (MacOS/Linux/Windows)

1. Go to the [releases](https://github.com/utkuozdemir/nvidia_gpu_exporter/releases) and download
   the latest release archive for your platform.
2. Extract the archive.
3. Move the binary to somewhere in your `PATH`.

Sample steps for Linux 64-bit:

```bash
VERSION=1.3.1
wget https://github.com/utkuozdemir/nvidia_gpu_exporter/releases/download/v${VERSION}/nvidia_gpu_exporter_${VERSION}_linux_x86_64.tar.gz
tar -xvzf nvidia_gpu_exporter_${VERSION}_linux_x86_64.tar.gz
mv nvidia_gpu_exporter /usr/bin
nvidia_gpu_exporter --help
```

### Verifying the download

Each release also ships a `checksums.txt` and a detached GPG signature
`checksums.txt.asc` that covers it. Import the public key once, then check the
signature and the checksum of what you downloaded:

```bash
curl -fsSL https://utkuozdemir.github.io/nvidia_gpu_exporter/pubkey.asc | gpg --import
gpg --verify checksums.txt.asc checksums.txt
sha256sum --ignore-missing -c checksums.txt
```

## Installing with winget (Windows)

On Windows the exporter is also available through
[winget](https://learn.microsoft.com/windows/package-manager/):

```PowerShell
winget install utkuozdemir.nvidia_gpu_exporter
```

That puts `nvidia_gpu_exporter` on your `PATH`, so you can run it directly.

If you instead want to run it as a service, use the machine-wide install (not the
per-user one above, so the service account can reach the binary and it keeps
working across upgrades), then register it as shown in
[Installing as a Windows Service](#installing-as-a-windows-service):

```PowerShell
winget install --scope machine utkuozdemir.nvidia_gpu_exporter
nvidia_gpu_exporter install
```

Use one or the other, not both.

## Installing as a Windows Service

The exporter speaks the Windows Service Control Manager protocol natively, so it
runs as a normal Windows service registered with its own `install` command. No
third-party service wrapper (such as NSSM) is required.

Get the binary with [Scoop](https://scoop.sh) or
[winget](#installing-with-winget-windows), or download it by hand. The steps
below use Scoop.

> [!NOTE]
> Earlier versions of this guide used [NSSM](https://nssm.cc) to run the exporter
> as a service. If you installed it that way before, remove the old service first
> from an administrator PowerShell prompt, then follow the steps below:
>
> ```PowerShell
> Stop-Service nvidia_gpu_exporter
> nssm remove nvidia_gpu_exporter confirm
> ```
>
> NSSM is no longer needed. If nothing else on the machine uses it, you can drop
> it too with `scoop uninstall nssm`.

1. If you don't have Scoop yet, open a regular PowerShell prompt and install it:

   ```PowerShell
   Set-ExecutionPolicy RemoteSigned -Scope CurrentUser
   Invoke-Expression (New-Object System.Net.WebClient).DownloadString('https://get.scoop.sh')
   ```

2. Open a PowerShell prompt as Administrator (right-click - Run as administrator).
3. Install the exporter and register it as a service:

   ```PowerShell
   # Install the binary with Scoop (globally, so the path is stable across updates).
   scoop install git
   scoop bucket add nvidia_gpu_exporter https://github.com/utkuozdemir/scoop_nvidia_gpu_exporter.git
   scoop install nvidia_gpu_exporter/nvidia_gpu_exporter --global

   # Register the service. It starts automatically on boot and restarts on failure.
   & 'C:\ProgramData\scoop\apps\nvidia_gpu_exporter\current\nvidia_gpu_exporter.exe' install

   # Allow the metrics port through the firewall, scoped to the local network.
   # Only needed if Prometheus scrapes from another machine. If Prometheus runs
   # on this same box, loopback is never firewalled and you can skip this.
   # Drop -RemoteAddress (or widen it) if your Prometheus is on another subnet.
   New-NetFirewallRule -DisplayName "Nvidia GPU Exporter" -Direction Inbound -Action Allow -Protocol TCP -LocalPort 9835 -Profile Private,Domain -RemoteAddress LocalSubnet

   # Start it.
   Start-Service nvidia_gpu_exporter
   ```

If you prefer not to use Scoop, download the Windows archive from the
[releases](https://github.com/utkuozdemir/nvidia_gpu_exporter/releases), extract
`nvidia_gpu_exporter.exe` somewhere stable (for example
`C:\Program Files\nvidia_gpu_exporter\`), and run the same `install` and
`Start-Service` commands using that path instead.

Any flags you pass to `install` are baked into the service command line, so you
configure the service exactly like you would when running it interactively. With
no flags it listens on `:9835`. For example, to use a different port:

```PowerShell
& 'C:\ProgramData\scoop\apps\nvidia_gpu_exporter\current\nvidia_gpu_exporter.exe' install --web.listen-address=:9836
```

Running `install` again reconfigures the existing service in place, so to change
the flags later just re-run it with the new ones (no need to uninstall first).
Restart the service afterwards for the new command line to take effect:

```PowerShell
Restart-Service nvidia_gpu_exporter
```

The service writes its logs to the Windows Event Log (the `Application` log,
under the source `nvidia_gpu_exporter`).

Manage and remove the service with the usual tooling:

```PowerShell
# Status, stop, start
Get-Service nvidia_gpu_exporter
Stop-Service nvidia_gpu_exporter
Start-Service nvidia_gpu_exporter

# Uninstall the service (stop it first). When the binary is on your PATH
# (Scoop or winget both put it there) you can call it by name; if you
# installed it by hand and it is not on your PATH, use the full path instead
# (for example .\nvidia_gpu_exporter.exe uninstall).
Stop-Service nvidia_gpu_exporter
nvidia_gpu_exporter uninstall

# Remove the firewall rule if you added one.
Remove-NetFirewallRule -DisplayName "Nvidia GPU Exporter"
```

If you installed the binary with winget, remove it afterwards with
`winget uninstall utkuozdemir.nvidia_gpu_exporter`. With Scoop, use
`scoop uninstall nvidia_gpu_exporter --global` (the same `--global` scope it
was installed with).

## Installing as a Linux (Systemd) Service

If your Linux distro is using systemd, you can install the exporter as a service using the unit file provided.

Follow these simple steps:

1. Download the Linux binary matching your CPU architecture and put it under `/usr/bin` directory.
2. Create a system user and group named `nvidia_gpu_exporter` for the service:

   ```bash
   sudo useradd --system --no-create-home --shell /usr/sbin/nologin nvidia_gpu_exporter
   ```

3. Drop a copy of the file **[nvidia_gpu_exporter.service](../install/systemd/nvidia_gpu_exporter.service)** under `/etc/systemd/system` directory.
4. Run `sudo systemctl daemon-reload`
5. Start and enable the service to run on boot: `sudo systemctl enable --now nvidia_gpu_exporter`

## Running in Docker

The container image does not bundle any NVIDIA components. Instead, the
[NVIDIA Container Toolkit](https://docs.nvidia.com/datacenter/cloud-native/container-toolkit/latest/install-guide.html)
injects the GPU devices, the driver libraries, and the `nvidia-smi` binary
matched to your host driver when the container starts. The same setup works
with any driver version, any number of GPUs, and any CPU architecture.

You will need:

- The NVIDIA driver installed on the host
- The NVIDIA Container Toolkit installed and configured for Docker

Then run:

```bash
docker run -d \
  --name nvidia_gpu_exporter \
  --restart unless-stopped \
  --gpus all \
  -e NVIDIA_DRIVER_CAPABILITIES=utility \
  -p 9835:9835 \
  utkuozdemir/nvidia_gpu_exporter:1.7.0
```

`--gpus all` turns on Docker's NVIDIA integration for the container and
exposes all GPUs to it. `NVIDIA_DRIVER_CAPABILITIES=utility` declares that
the container only needs the `nvidia-smi`/NVML tier. Recent toolkit versions
inject the full driver userspace either way, so treat the variable as
documentation of intent and compatibility with older setups rather than a
restriction.

> [!TIP]
> The Docker image is also available from GHCR as `ghcr.io/utkuozdemir/nvidia_gpu_exporter`

With docker-compose:

```yaml
services:
  nvidia_gpu_exporter:
    image: utkuozdemir/nvidia_gpu_exporter:1.7.0
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
> not select it. Without `--gpus`, a device reservation, or `runtime: nvidia`,
> the container runs on the default runtime and no GPU access is injected. In
> that case the exporter still comes up, but it serves only its own health
> metrics with `nvidia_smi_last_collect_success 0`.

### Verifying the image

The images on Docker Hub and GHCR are signed keyless with cosign. Verify one
(replace `VERSION` with a tag released after signing was introduced) with:

```bash
cosign verify \
  --certificate-oidc-issuer=https://token.actions.githubusercontent.com \
  --certificate-identity-regexp='^https://github\.com/utkuozdemir/nvidia_gpu_exporter/\.github/workflows/release\.yml@refs/tags/v.*$' \
  utkuozdemir/nvidia_gpu_exporter:VERSION
```

### Fallback without the NVIDIA Container Toolkit

On hosts where the toolkit cannot be installed, you can mount the required
pieces into the container yourself: each `/dev/nvidia*` device, the
`nvidia-smi` binary, and the `libnvidia-ml.so*` library files from the host
library directory. Be warned that this is fragile: the library symlink chain
breaks on driver upgrades, the device list varies with GPU count, and library
paths differ per distribution and architecture. Prefer the toolkit whenever
possible.

## Running in Kubernetes

Run the exporter as a DaemonSet with the NVIDIA runtime. The runtime then
injects GPU access on each node, the same way it does for Docker above.

> [!IMPORTANT]
> Do **not** request an `nvidia.com/gpu` resource for the exporter. The
> Kubernetes device plugin allocates whole GPUs exclusively, so a monitoring
> pod that requests one takes that GPU away from real workloads. The
> environment variable approach below gives the exporter visibility of all
> GPUs on the node without reserving any of them.

The easiest way is the [Helm chart](../charts/nvidia-gpu-exporter), which lives
in this repository and implements all of the above:

```bash
helm install nvidia-gpu-exporter oci://ghcr.io/utkuozdemir/charts/nvidia-gpu-exporter \
  --set runtimeClassName=nvidia
```

See the [chart README](../charts/nvidia-gpu-exporter/README.md) for the full
values reference, the optional monitoring extras (ServiceMonitor, PodMonitor,
alerts, the Grafana dashboard), and the migration notes if you are coming
from the old chart repository.

If you prefer not to use Helm, a minimal DaemonSet:

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
          image: utkuozdemir/nvidia_gpu_exporter:1.7.0
          env:
            - name: NVIDIA_VISIBLE_DEVICES
              value: all
            - name: NVIDIA_DRIVER_CAPABILITIES
              value: utility
          ports:
            - containerPort: 9835
              name: metrics
```

The nodes need the NVIDIA driver and the NVIDIA Container Toolkit configured
for the container runtime. Managed GPU node images (AKS, GKE, EKS) typically
ship both. On self-managed nodes with containerd (including k3s), install the
toolkit and either make the NVIDIA runtime the default or create a
`RuntimeClass` named `nvidia` and reference it as above.

### Managed Kubernetes (AKS and similar)

GPU node images on managed Kubernetes ship the NVIDIA driver and container
toolkit as part of the node image, so the DaemonSet approach above works
as-is. The `nvidia.com/gpu` resource and the NVIDIA device plugin exist for
scheduling GPU workloads, and the exporter deliberately stays out of that
mechanism since it only needs to observe the GPUs. Allocation limitations
such as whole-number allocations and one workload per GPU therefore apply to
your workloads but never to the exporter.
