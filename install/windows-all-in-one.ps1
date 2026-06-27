# All-in-one Windows installer: nvidia_gpu_exporter (as a native Windows service)
# plus Prometheus and Grafana, with the dashboard and datasource provisioned.
#
# The exporter speaks the Windows service control manager protocol itself, so it
# needs no wrapper. Prometheus and Grafana do not, so they run under WinSW, a
# small, widely used service wrapper.
#
# Run this from an elevated PowerShell prompt (Run as administrator).

$ErrorActionPreference = "Stop"

$isAdmin = ([Security.Principal.WindowsPrincipal][Security.Principal.WindowsIdentity]::GetCurrent()).IsInRole([Security.Principal.WindowsBuiltinRole]::Administrator)
if (-Not $isAdmin) {
    throw "This script must be run from an elevated PowerShell prompt (Run as administrator)."
}

# Resolve the global scoop directory, honoring a customized SCOOP_GLOBAL.
$ScoopGlobal = if ($env:SCOOP_GLOBAL) { $env:SCOOP_GLOBAL } else { "C:\ProgramData\scoop" }
$ServicesRoot = "C:\ProgramData\nvidia_gpu_exporter\services"

# Pin WinSW to a known-good version and checksum. WinSW v3 changes the config
# format, so always-latest could silently break the v2-style config below.
$WinSWVersion = "v2.12.0"
$WinSWSha256 = "05B82D46AD331CC16BDC00DE5C6332C1EF818DF8CEEFCD49C726553209B3A0DA"

# Download WinSW once. The x64 build targets .NET Framework, which ships with
# Windows 10 and 11, so there is nothing else to install.
New-Item -ItemType Directory -Force -Path $ServicesRoot | Out-Null
$WinSW = "$ServicesRoot\WinSW-x64.exe"
if (-Not (Test-Path $WinSW)) {
    Invoke-WebRequest `
      -Uri "https://github.com/winsw/winsw/releases/download/$WinSWVersion/WinSW-x64.exe" `
      -OutFile $WinSW -UseBasicParsing
}
# Verify the checksum every run, so a corrupted or tampered binary is never
# registered as a service.
$actualSha = (Get-FileHash -Algorithm SHA256 $WinSW).Hash
if ($actualSha -ne $WinSWSha256) {
    Remove-Item -Force $WinSW -ErrorAction SilentlyContinue
    throw "WinSW checksum mismatch: expected $WinSWSha256 but got $actualSha."
}

# Remove a service of this name whose binary is not the one we expect, for
# example a service left behind by an older nssm-based version of this script, so
# it can be recreated cleanly instead of being restarted as-is.
function Remove-LegacyService {
    param(
        [string] $Id,
        [string] $ExpectedPathFragment
    )
    if (-Not (Get-Service $Id -ErrorAction SilentlyContinue)) { return }
    $path = (Get-CimInstance Win32_Service -Filter "Name='$Id'").PathName
    if ($path -notlike "*$ExpectedPathFragment*") {
        Stop-Service $Id -ErrorAction SilentlyContinue
        & sc.exe delete $Id | Out-Null
        $tries = 0
        while ((Get-Service $Id -ErrorAction SilentlyContinue) -and $tries -lt 30) {
            Start-Sleep -Milliseconds 300
            $tries++
        }
    }
}

# Install (or update) a Windows service that wraps a plain executable via WinSW.
function Install-WrappedService {
    param(
        [string] $Id,
        [string] $DisplayName,
        [string] $Description,
        [string] $Executable,
        [string] $Arguments,
        [string] $WorkingDirectory
    )
    $dir = Join-Path $ServicesRoot $Id
    $wrapper = Join-Path $dir "$Id.exe"
    New-Item -ItemType Directory -Force -Path $dir | Out-Null
    # Replace any non-WinSW service of this name (such as an old nssm one).
    Remove-LegacyService -Id $Id -ExpectedPathFragment $wrapper
    # Stop first if already running, so the wrapper exe is not locked when we
    # overwrite it. This keeps re-running the whole script safe.
    Stop-Service $Id -ErrorAction SilentlyContinue
    Copy-Item -Force $WinSW $wrapper
    $config = @"
<service>
  <id>$Id</id>
  <name>$DisplayName</name>
  <description>$Description</description>
  <executable>$Executable</executable>
  <arguments>$Arguments</arguments>
  <workingdirectory>$WorkingDirectory</workingdirectory>
  <onfailure action="restart" delay="5 sec" />
  <log mode="roll" />
</service>
"@
    Set-Content -Path (Join-Path $dir "$Id.xml") -Value $config
    if (-Not (Get-Service $Id -ErrorAction SilentlyContinue)) {
        & $wrapper install
    }
    # WinSW reads its config at start, so starting now picks up any change.
    Start-Service $Id
}

# Scoop. Git is required for "scoop bucket add". get.scoop.sh refuses to install
# under an elevated prompt unless -RunAsAdmin is passed, and this script is
# elevated, so opt in explicitly.
if (-Not (Get-Command "scoop" -ErrorAction SilentlyContinue)) {
    Invoke-Expression "& {$(Invoke-RestMethod -Uri https://get.scoop.sh)} -RunAsAdmin"
    # Make the freshly installed scoop available in the current session.
    $env:Path = [Environment]::GetEnvironmentVariable("Path", "Machine") + ";" + [Environment]::GetEnvironmentVariable("Path", "User")
}
scoop install git
scoop bucket add extras

# Exporter: install the binary and register it as a native Windows service.
$ExporterExe = "$ScoopGlobal\apps\nvidia_gpu_exporter\current\nvidia_gpu_exporter.exe"
scoop bucket add nvidia_gpu_exporter https://github.com/utkuozdemir/scoop_nvidia_gpu_exporter.git
scoop install nvidia_gpu_exporter/nvidia_gpu_exporter --global
# Replace an old nssm exporter service, then register (or reconfigure) the
# native one in place.
Remove-LegacyService -Id "nvidia_gpu_exporter" -ExpectedPathFragment "nvidia_gpu_exporter.exe"
& $ExporterExe install
Restart-Service nvidia_gpu_exporter -ErrorAction SilentlyContinue
Start-Service nvidia_gpu_exporter

# Prometheus
$PrometheusConfig = @"
global:
  scrape_interval:     15s
  evaluation_interval: 15s
scrape_configs:
  - job_name: 'prometheus'
    static_configs:
    - targets: ['localhost:9090']
  - job_name: 'nvidia_gpu_exporter'
    static_configs:
    - targets: ['localhost:9835']
"@
scoop install prometheus --global
Set-Content -Path "$ScoopGlobal\apps\prometheus\current\prometheus.yml" -Value $PrometheusConfig
Install-WrappedService `
  -Id "prometheus" `
  -DisplayName "Prometheus" `
  -Description "Prometheus monitoring server" `
  -Executable "$ScoopGlobal\apps\prometheus\current\prometheus.exe" `
  -Arguments "" `
  -WorkingDirectory "$ScoopGlobal\apps\prometheus\current"

# Grafana
$GrafanaDataSourceConfig = @"
apiVersion: 1
datasources:
  - name: Prometheus
    type: prometheus
    access: proxy
    orgId: 1
    uid: prometheus
    url: http://localhost:9090
    isDefault: true
    version: 1
    editable: false
"@
# Keep the dashboard files outside the scoop "current" junction: Grafana's
# provisioning cannot resolve a path that goes through a Windows junction.
$DashboardDir = "C:\ProgramData\nvidia_gpu_exporter\grafana-dashboards"
$GrafanaDashboardProvider = @"
apiVersion: 1
providers:
  - name: nvidia_gpu_exporter
    orgId: 1
    folder: ''
    type: file
    disableDeletion: false
    editable: true
    options:
      path: $DashboardDir
"@
$GrafanaHome = "$ScoopGlobal\apps\grafana\current"
scoop install grafana --global

# Provision the Prometheus datasource with a fixed uid so the dashboard can
# reference it without any manual import step.
New-Item -ItemType Directory -Force -Path "$GrafanaHome\conf\provisioning\datasources" | Out-Null
Set-Content `
  -Path "$GrafanaHome\conf\provisioning\datasources\prometheus.yaml" `
  -Value $GrafanaDataSourceConfig

# Provision the dashboard via a file provider. The dashboard is a grafana.com
# export, so its datasource is a "${DS_PROMETHEUS}" input placeholder. Resolve it
# to the provisioned datasource uid so it loads automatically.
New-Item -ItemType Directory -Force -Path "$GrafanaHome\conf\provisioning\dashboards" | Out-Null
Set-Content `
  -Path "$GrafanaHome\conf\provisioning\dashboards\nvidia_gpu_exporter.yaml" `
  -Value $GrafanaDashboardProvider
New-Item -ItemType Directory -Force -Path $DashboardDir | Out-Null
$dashboard = (Invoke-WebRequest `
  -Uri https://raw.githubusercontent.com/utkuozdemir/nvidia_gpu_exporter/main/grafana/dashboard.json `
  -UseBasicParsing).Content
$dashboard = $dashboard -replace '\$\{DS_PROMETHEUS\}', 'prometheus'
Set-Content -Path "$DashboardDir\nvidia_gpu_exporter.json" -Value $dashboard

# Modern Grafana ships a single "grafana.exe" run as "grafana.exe server". The
# home path makes it find conf\provisioning and write its data under the install.
Install-WrappedService `
  -Id "grafana" `
  -DisplayName "Grafana" `
  -Description "Grafana visualization server" `
  -Executable "$GrafanaHome\bin\grafana.exe" `
  -Arguments "server --homepath $GrafanaHome" `
  -WorkingDirectory "$GrafanaHome"
