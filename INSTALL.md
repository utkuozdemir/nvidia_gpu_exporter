# Installation

There are many different installation methods for different use cases.

### All-in-One Windows Installation

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


### By downloading the binaries (MacOS/Linux/Windows)

1. Go to the [releases](https://github.com/utkuozdemir/nvidia_gpu_exporter/releases) and download
   the latest release archive for your platform.
2. Extract the archive.
3. Move the binary to somewhere in your `PATH`.

Sample steps for Linux 64-bit:
```bash
$ VERSION=0.3.0
$ wget https://github.com/utkuozdemir/nvidia_gpu_exporter/releases/download/v${VERSION}/nvidia_gpu_exporter_${VERSION}_linux_x86_64.tar.gz
$ tar -xvzf nvidia_gpu_exporter_${VERSION}_linux_x86_64.tar.gz
$ mv nvidia_gpu_exporter /usr/local/bin
$ nvidia_gpu_exporter --help
```

### Installing as a Windows Service

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


### Installing as a Linux (Systemd) Service

If your Linux distro is using systemd, you can install the exporter as a service using the unit file provided.

Follow these simple steps:
1. Download the Linux binary matching your CPU architecture and put it under `/usr/local/bin` directory.
2. Drop a copy of the file **[nvidia_gpu_exporter.service](systemd/nvidia_gpu_exporter.service)** under `/etc/systemd/system` directory.
3. Run `sudo systemctl daemon-reload`
4. Start and enable the service to run on boot: `sudo systemctl enable --now nvidia_gpu_exporter`

### Running in Docker
You can run the exporter in a Docker container.

For it to work, you will need to ensure the following:
- The `nvidia-smi` binary is bind-mounted from the host to the container under its `PATH`
- The devices `/dev/nvidiaX` (depends on the number of GPUs you have) and `/dev/nvidiactl` are mounted into the container
- The library files `libnvidia-ml.so` and `libnvidia-ml.so.1` are mounted inside the container.
  They are typically found under `/usr/lib/x86_64-linux-gnu/` or `/usr/lib/i386-linux-gnu/`.
  Locate them in your host to ensure you are mounting them from the correct path.

A working example with all these combined (tested in `Ubuntu 20.04`):
```bash
docker run -d \
--name nvidia_smi_exporter \
--restart unless-stopped \
--device /dev/nvidiactl:/dev/nvidiactl \
--device /dev/nvidia0:/dev/nvidia0 \
-v /usr/lib/x86_64-linux-gnu/libnvidia-ml.so:/usr/lib/x86_64-linux-gnu/libnvidia-ml.so \
-v /usr/lib/x86_64-linux-gnu/libnvidia-ml.so.1:/usr/lib/x86_64-linux-gnu/libnvidia-ml.so.1 \
-v /usr/bin/nvidia-smi:/usr/bin/nvidia-smi \
-p 9835:9835 \
utkuozdemir/nvidia_gpu_exporter:0.3.0
```

### Running in Kubernetes
Using the exporter in Kubernetes is pretty similar with running it in Docker.

You can use the [official helm chart](https://artifacthub.io/packages/helm/utkuozdemir/nvidia-gpu-exporter) to install the exporter.

The chart was tested on the following configuration:
- Ubuntu Desktop 20.04 with Kernel `5.8.0-55-generic`
- K3s `v1.21.1+k3s1`
- Nvidia GeForce RTX 2080 Super
- Nvidia Driver version `465.27`

**Note:** I didn't have chance to test it on an enterprise cluster with GPU support.
If you have access to one and give the exporter a try and share the results, I would appreciate it greatly.
