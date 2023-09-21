# Installation

There are many installation methods for different use cases.

## All-in-One Windows Installation

If you use Windows and not familiar with the tools like Prometheus/Grafana,
you can simply use the PowerShell installation script to get the Exporter,
Prometheus and Grafana installed on the same machine.

Follow the steps below:

1. Download [the installation script](https://raw.githubusercontent.com/utkuozdemir/nvidia_gpu_exporter/master/install/windows.ps1) (save it with `.ps1` extension)
2. Open an administrative PowerShell prompt (search for PowerShell in the start menu - right-click - Run as Administrator)
3. In the prompt, execute the script you have downloaded. For example, `C:\Users\<YOUR_USERNAME>\Downloads\windows.ps1`
4. Verify that you have Prometheus running by opening [http://localhost:9090](http://localhost:9090) in your browser.
5. Verify that you have Grafana running by opening [http://localhost:3000](http://localhost:3000) in your browser.
6. Login to Grafana using the initial credentials: `admin` - `admin`. Set a new password if you like.
7. On Grafana, choose the option "Create - Import" from the top-left (big plus sign).
8. Enter `14574` to the ID field and click "Load".
9. Finally, choose "Prometheus" as data source from the dropdown. Hit "import".
10. Enjoy the dashboard!

## Using .deb or .rpm packages

If you are on a Debian-based system (.deb), you can install the exporter with the following command:

```bash
sudo dpkg -i nvidia-gpu-exporter_1.1.0_linux_amd64.deb
```

If you are on a Red Hat-based system (.rpm), you can install the exporter with the following command:

```bash
sudo rpm -i nvidia-gpu-exporter_1.1.0_linux_amd64.rpm
```

**Note:** .rpm and .deb packages only support systems using systemd as init system.

## By downloading the binaries (MacOS/Linux/Windows)

1. Go to the [releases](https://github.com/utkuozdemir/nvidia_gpu_exporter/releases) and download
   the latest release archive for your platform.
2. Extract the archive.
3. Move the binary to somewhere in your `PATH`.

Sample steps for Linux 64-bit:

```bash
VERSION=1.1.0
wget https://github.com/utkuozdemir/nvidia_gpu_exporter/releases/download/v${VERSION}/nvidia_gpu_exporter_${VERSION}_linux_x86_64.tar.gz
tar -xvzf nvidia_gpu_exporter_${VERSION}_linux_x86_64.tar.gz
mv nvidia_gpu_exporter /usr/bin
nvidia_gpu_exporter --help
```

## Installing as a Windows Service

To install the exporter as a Windows service, follow the steps below:

1. Open a privileged powershell prompt (right click - Run as administrator)
2. Run the following commands:

```powershell
Invoke-Expression (New-Object System.Net.WebClient).DownloadString('https://get.scoop.sh')
scoop install nssm --global
scoop bucket add nvidia_gpu_exporter https://github.com/utkuozdemir/scoop_nvidia_gpu_exporter.git
scoop install nvidia_gpu_exporter/nvidia_gpu_exporter --global
New-NetFirewallRule -DisplayName "Nvidia GPU Exporter" -Direction Inbound -Action Allow -Protocol TCP -LocalPort 9835
nssm install nvidia_gpu_exporter "C:\ProgramData\scoop\apps\nvidia_gpu_exporter\current\nvidia_gpu_exporter.exe"
Start-Service nvidia_gpu_exporter
```

These steps do the following:

- Installs [Scoop package manager](https://scoop.sh)
- Installs [NSSM - a service manager](https://nssm.cc/download) using Scoop
- Installs the exporter using Scoop
- Exposes app's TCP port (`9835`) to be accessible from Windows Firewall
- Installs the exporter as a Windows service using NSSM
- Starts the installed service

## Installing as a Linux (Systemd) Service

If your Linux distro is using systemd, you can install the exporter as a service using the unit file provided.

Follow these simple steps:

1. Download the Linux binary matching your CPU architecture and put it under `/usr/bin` directory.
2. Drop a copy of the file **[nvidia_gpu_exporter.service](systemd/nvidia_gpu_exporter.service)** under `/etc/systemd/system` directory.
3. Run `sudo systemctl daemon-reload`
4. Start and enable the service to run on boot: `sudo systemctl enable --now nvidia_gpu_exporter`

## Running in Docker

You can run the exporter in a Docker container.

For it to work, you will need to ensure the following:

- [nvidia-container-toolkit](https://docs.nvidia.com/datacenter/cloud-native/container-toolkit/latest/install-guide.html) installed.
- execute `docker run --rm --gpus all nvidia/cuda:11.6.2-base-ubuntu20.04 nvidia-smi` can get the expected `nvidia-smi` response.

A working example with all these combined (tested in `Debian 11`):

```bash
docker run -d \
  --name nvidia_smi_exporter \
  --restart unless-stopped \
  --gpus all \
  -p 9835:9835 \
  utkuozdemir/nvidia_gpu_exporter:1.3.0
```

### Docker Compose

As shown in [docker-compose.yml](./docker-compose.yml), download it and execute `docker compose up -d`.

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

- The AKS node Ubuntu image already packages the GPU drivers, [here](https://github.com/Azure/AKS/blob/master/vhd-notes/aks-ubuntu/AKSUbuntu-1804/2022.03.03.txt).
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
