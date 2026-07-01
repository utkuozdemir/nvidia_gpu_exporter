# Installation

There are many installation methods for different use cases.

## All-in-One Windows Installation

If you use Windows and not familiar with the tools like Prometheus/Grafana,
you can simply use the PowerShell installation script to get the Exporter,
Prometheus and Grafana installed on the same machine.

Follow the steps below:

1. Download [the installation script](https://raw.githubusercontent.com/utkuozdemir/nvidia_gpu_exporter/main/install/windows-all-in-one.ps1) (save it with `.ps1` extension)
2. Open an administrative PowerShell prompt (search for PowerShell in the start menu - right-click - Run as Administrator)
3. In the prompt, execute the script you have downloaded. For example, `C:\Users\<YOUR_USERNAME>\Downloads\windows-all-in-one.ps1`
4. Verify that you have Prometheus running by opening [http://localhost:9090](http://localhost:9090) in your browser.
5. Verify that you have Grafana running by opening [http://localhost:3000](http://localhost:3000) in your browser.
6. Login to Grafana using the initial credentials: `admin` - `admin`. Set a new password if you like.
7. The Prometheus datasource and the "Nvidia GPU Metrics" dashboard are already provisioned. Open the dashboard from the Dashboards list and enjoy!

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

3. Drop a copy of the file **[nvidia_gpu_exporter.service](systemd/nvidia_gpu_exporter.service)** under `/etc/systemd/system` directory.
4. Run `sudo systemctl daemon-reload`
5. Start and enable the service to run on boot: `sudo systemctl enable --now nvidia_gpu_exporter`

## Running in Docker

You can run the exporter in a Docker container.

For it to work, you will need to ensure the following:

- The `nvidia-smi` binary is bind-mounted from the host to the container under its `PATH`
- The devices `/dev/nvidiaX` (depends on the number of GPUs you have) and `/dev/nvidiactl` are mounted into the container
- The library files `libnvidia-ml.so` and `libnvidia-ml.so.1` are mounted inside the container.
  They are typically found under `/usr/lib/x86_64-linux-gnu/` or `/usr/lib/i386-linux-gnu/`.
  Locate them in your host to ensure you are mounting them from the correct path.

A working example with all these combined (tested in `Ubuntu 20.04`):

```bash
$ docker run -d \
--name nvidia_smi_exporter \
--restart unless-stopped \
--device /dev/nvidiactl:/dev/nvidiactl \
--device /dev/nvidia0:/dev/nvidia0 \
-v /usr/lib/x86_64-linux-gnu/libnvidia-ml.so:/usr/lib/x86_64-linux-gnu/libnvidia-ml.so \
-v /usr/lib/x86_64-linux-gnu/libnvidia-ml.so.1:/usr/lib/x86_64-linux-gnu/libnvidia-ml.so.1 \
-v /usr/bin/nvidia-smi:/usr/bin/nvidia-smi \
-p 9835:9835 \
utkuozdemir/nvidia_gpu_exporter:1.3.1
```

> [!TIP]
> The Docker image is also available from GHCR as `ghcr.io/utkuozdemir/nvidia_gpu_exporter`

## Running in Kubernetes

Using the exporter in Kubernetes is pretty similar with running it in Docker.

You can use the [official helm chart](https://artifacthub.io/packages/helm/utkuozdemir/nvidia-gpu-exporter) to install the exporter.

The chart was tested on the following configuration:

- Ubuntu Desktop 20.04 with Kernel `5.8.0-55-generic`
- K3s `v1.21.1+k3s1`
- Nvidia GeForce RTX 2080 Super
- Nvidia Driver version `465.27`

### Running on Azure Kubernetes (AKS)

- NCasT4_v3 series VM node
- Nvidia Tesla T4 GPU
- Nvidia Driver version `470.57.02`
- Ubuntu `18.04` node image

By default, GPU resource allocations must be whole numbers. Unlike CPU and memory allocations, GPUs cannot be subdivided into smaller increments.

**Multi Instance GPU (MIG) configurations are not in scope of these notes.**

**The GPU allocation limitations apply to all instances of Kubernetes, regardless of vendor.**

```yaml
limits:
  memory: 1Gi
  cpu: 1000m
  nvidia.com/gpu: "1"
```

Unless the node has multiple GPUs, only a single GPU enabled deployment can run per node, assuming the node has a single GPU. This means that the `nvidia_gpu_exporter` cannot be run as a separate deployment or as a sidecar because it will be unable to schedule the GPU.

#### Driver Installation

This is a particularly vague area in Nvidia's fragmented documentation and while there are several articles online outlining Nvidia driver installation on Ubuntu and other distros, there is little that explains how this works in managed Kubernetes.

Containerized setups require:

- Nvidia GPU drivers for the specific Linux distribution
- Nvidia Container Toolkit

In AKS this manifests as:

- The AKS node Ubuntu image already packages the GPU drivers, as listed in the [AKS Ubuntu VHD release notes](https://github.com/Azure/AKS/blob/master/vhd-notes/aks-ubuntu/AKSUbuntu-1804/2022.03.03.txt).
- The [Nvidia Device Plugin](https://docs.microsoft.com/en-us/azure/aks/gpu-cluster#manually-install-the-nvidia-device-plugin) exposes the GPU to containers requesting GPU resources.

Testing locally and within several VMs on Azure confirms that the drivers are packaged with the VM image and do not need to be installed separately.

For example running a Docker image locally with no GPU drivers using `docker exec -ti` confirms no drivers present in `/usr/lib/x86_64-linux-gnu`.

Running the same image on an AKS GPU node reveals the following GPU driver files, confirming the driver injection using node OS image and Nvidia Driver Plugin.

```bash
libnvidia-allocator.so.1 -> libnvidia-allocator.so.470.57.02
libnvidia-allocator.so.470.57.02
libnvidia-cfg.so.1 -> libnvidia-cfg.so.470.57.02
libnvidia-cfg.so.470.57.02
libnvidia-compiler.so.470.57.02
libnvidia-ml.so.1 -> libnvidia-ml.so.470.57.02
libnvidia-ml.so.470.57.02
libnvidia-opencl.so.1 -> libnvidia-opencl.so.470.57.02
libnvidia-opencl.so.470.57.02
libnvidia-ptxjitcompiler.so.1 -> libnvidia-ptxjitcompiler.so.470.57.02
libnvidia-ptxjitcompiler.so.470.57.02
```

Additionally, we can now see the following in `/usr/bin`

```bash
nvidia-cuda-mps-control
nvidia-cuda-mps-server
nvidia-debugdump
nvidia-persistenced
nvidia-smi
```

#### Packaging and Deployment

Taking the above into account, we can embed the `/usr/bin/nvidia_gpu_exporter` into a GPU enabled deployment through a multi-stage Docker build using the `utkuozdemir/nvidia_gpu_exporter:0.5.0` as the base image.

Extending the Docker entrypoint with:

`/usr/bin/nvidia_gpu_exporter --web.listen-address=:9835 --web.telemetry-path=/metrics --nvidia-smi-command=nvidia-smi --log.level=info --query-field-names=AUTO --log.format=logfmt &`

This reduces overall complexity, inherits the packaged drivers & nvidia-smi, and most importantly leverages the same GPU resource request as the deployment/GPU you are trying to monitor.

**It is recommended to add logic to only start the `nvidia_gpu_exporter` if an Nvidia GPU is detected.**
