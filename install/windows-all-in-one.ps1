# All-in-one Windows installer: nvidia_gpu_exporter (as a native Windows service)
# plus Prometheus and Grafana, with the dashboard and datasource provisioned.
#
# The exporter speaks the Windows service control manager protocol itself, so it
# needs no wrapper. Grafana's MSI (installed via winget) registers its own
# service. Prometheus ships no service support, so it runs under WinSW, a small,
# widely used service wrapper.
#
# Run this from an elevated PowerShell prompt (Run as administrator).
# Re-running is safe: it updates the exporter to the latest release and leaves
# Prometheus data and Grafana state in place.

$ErrorActionPreference = "Stop"
# The byte-counting progress bar slows Invoke-WebRequest downloads to a crawl on
# Windows PowerShell 5.1.
$ProgressPreference = "SilentlyContinue"

$isAdmin = ([Security.Principal.WindowsPrincipal][Security.Principal.WindowsIdentity]::GetCurrent()).IsInRole([Security.Principal.WindowsBuiltinRole]::Administrator)
if (-Not $isAdmin) {
    throw "This script must be run from an elevated PowerShell prompt (Run as administrator)."
}

# Grafana comes from winget. Fail before touching anything, so a machine without
# winget (Windows Server, LTSC, blocked Store) is not left half installed.
if (-Not (Get-Command winget -ErrorAction SilentlyContinue)) {
    throw "winget is required (it installs Grafana) but was not found. Install 'App Installer' from the Microsoft Store and re-run."
}

# Windows PowerShell 5.1 may still default to TLS 1.0, which GitHub rejects.
# Enable TLS 1.2 for this session without dropping other enabled protocols.
[Net.ServicePointManager]::SecurityProtocol = [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12
# Pass the signed-in user's credentials to an authenticating proxy, so the
# downloads also work in corporate environments.
if ($null -ne [System.Net.WebRequest]::DefaultWebProxy) {
    [System.Net.WebRequest]::DefaultWebProxy.Credentials = [System.Net.CredentialCache]::DefaultCredentials
}

$ExporterHome = "$env:ProgramFiles\nvidia_gpu_exporter"
$PrometheusHome = "$env:ProgramFiles\Prometheus"

# Pin Prometheus to the current LTS release, and WinSW to a known-good version.
# WinSW v3 changes the config format, so always-latest could silently break the
# v2-style config below. Both downloads are checksum-verified.
$PrometheusVersion = "3.5.5"
$PrometheusSha256 = "FB262C2F2D4E55A59B2CC04810D31B5907DB1B1107677EA9D788B3E1B77E255D"
$WinSWVersion = "v2.12.0"
$WinSWSha256 = "05B82D46AD331CC16BDC00DE5C6332C1EF818DF8CEEFCD49C726553209B3A0DA"

$Temp = Join-Path $env:TEMP "nvidia_gpu_exporter-all-in-one"
New-Item -ItemType Directory -Force -Path $Temp | Out-Null

# Download a file, retrying transient network failures.
function Invoke-Download {
    param(
        [string] $Uri,
        [string] $OutFile
    )
    $attempts = 3
    for ($i = 1; $i -le $attempts; $i++) {
        try {
            Invoke-WebRequest -Uri $Uri -OutFile $OutFile -UseBasicParsing
            return
        } catch {
            Remove-Item -Force $OutFile -ErrorAction SilentlyContinue
            if ($i -eq $attempts) { throw }
            Write-Host "Download of $Uri failed ($($_.Exception.Message)), retrying..."
            Start-Sleep -Seconds 3
        }
    }
}

# Ensure a file with the given SHA256 exists at the path: keep a matching
# existing file, replace a mismatching or missing one, and fail hard when the
# fresh download does not match either, so a corrupted download is never
# installed or registered as a service.
function Get-VerifiedFile {
    param(
        [string] $Uri,
        [string] $OutFile,
        [string] $Sha256
    )
    if (Test-Path $OutFile) {
        if ((Get-FileHash -Algorithm SHA256 $OutFile).Hash -eq $Sha256) { return }
        Remove-Item -Force $OutFile
    }
    Invoke-Download -Uri $Uri -OutFile $OutFile
    $actual = (Get-FileHash -Algorithm SHA256 $OutFile).Hash
    if ($actual -ne $Sha256) {
        Remove-Item -Force $OutFile -ErrorAction SilentlyContinue
        throw "Checksum mismatch for ${Uri}: expected $Sha256 but got $actual."
    }
}

# Windows PowerShell 5.1 writes ANSI by default, so generated configuration is
# written explicitly as UTF-8 (without a byte order mark) instead.
function Write-TextFile {
    param(
        [string] $Path,
        [string] $Content
    )
    [System.IO.File]::WriteAllText($Path, $Content)
}

# Refuse to proceed when a service this script is about to create or adopt
# already exists with a binary that is not the expected one, so nothing that
# happens to share a name is ever reconfigured or destroyed. The uninstall
# script removes installations made by this script or its older nssm- and
# scoop-based versions.
function Assert-ServiceOwnership {
    param(
        [string] $Id,
        [string] $ExpectedPathFragment
    )
    if (-Not (Get-Service $Id -ErrorAction SilentlyContinue)) { return }
    $path = (Get-CimInstance Win32_Service -Filter "Name='$Id'").PathName
    if ($path -like "*$ExpectedPathFragment*") { return }
    throw "A service named '$Id' already exists (binary: $path). If it is from an earlier version of this setup, run the uninstall script first (see the install guide); otherwise remove or rename the service. Then re-run."
}

# Check all service names up front, before anything is installed, so a
# conflicting service aborts the run while the machine is still untouched. The
# "grafana" check matters for the MSI too: Windows service names are case
# insensitive, so a foreign "grafana" service would collide with the "Grafana"
# service the MSI registers.
$ExporterExe = Join-Path $ExporterHome "nvidia_gpu_exporter.exe"
$WinSW = Join-Path $PrometheusHome "prometheus-service.exe"
Assert-ServiceOwnership -Id "nvidia_gpu_exporter" -ExpectedPathFragment $ExporterExe
Assert-ServiceOwnership -Id "prometheus" -ExpectedPathFragment $WinSW
Assert-ServiceOwnership -Id "grafana" -ExpectedPathFragment "GrafanaLabs"

# --- Exporter -----------------------------------------------------------------
# Direct-download the latest GitHub release and register it as a native Windows
# service. The release's checksums file pins the exact version and the expected
# hash of the zip, so the downloaded archive's integrity is verified.

Write-Host "Installing nvidia_gpu_exporter..."
$checksumsFile = Join-Path $Temp "checksums.txt"
Remove-Item -Force $checksumsFile -ErrorAction SilentlyContinue
Invoke-Download `
  -Uri "https://github.com/utkuozdemir/nvidia_gpu_exporter/releases/latest/download/checksums.txt" `
  -OutFile $checksumsFile
$checksums = [System.IO.File]::ReadAllText($checksumsFile)
if ($checksums -notmatch '(?m)^(?<sha>[0-9a-f]{64})\s+(?<file>nvidia_gpu_exporter_(?<version>[0-9][^_]*)_windows_x86_64\.zip)\s*$') {
    throw "Could not find the Windows zip in the release checksums file."
}
$zipFile = $Matches.file
$zipSha = $Matches.sha.ToUpperInvariant()
$version = $Matches.version

$exporterZip = Join-Path $Temp $zipFile
Get-VerifiedFile `
  -Uri "https://github.com/utkuozdemir/nvidia_gpu_exporter/releases/download/v$version/$zipFile" `
  -OutFile $exporterZip -Sha256 $zipSha
$exporterExtract = Join-Path $Temp "exporter"
Remove-Item -Recurse -Force $exporterExtract -ErrorAction SilentlyContinue
Expand-Archive -Path $exporterZip -DestinationPath $exporterExtract -Force

# Stop the current service so the exe is not locked while it is overwritten.
Stop-Service nvidia_gpu_exporter -ErrorAction SilentlyContinue
New-Item -ItemType Directory -Force -Path $ExporterHome | Out-Null
Copy-Item -Force (Join-Path $exporterExtract "nvidia_gpu_exporter.exe") $ExporterExe

# The exporter's own install subcommand registers (or reconfigures) the service.
& $ExporterExe install
if ($LASTEXITCODE -ne 0) { throw "nvidia_gpu_exporter install failed with exit code $LASTEXITCODE." }
Start-Service nvidia_gpu_exporter

# --- Prometheus ---------------------------------------------------------------
# Direct-download the pinned release and wrap it with WinSW. The time series
# data lives under the data subdirectory and survives re-runs and upgrades.

Write-Host "Installing Prometheus..."
$PrometheusDist = "prometheus-$PrometheusVersion.windows-amd64"
$prometheusVersionFile = Join-Path $PrometheusHome ".installed-version"
$installedPrometheus = if (Test-Path $prometheusVersionFile) { Get-Content $prometheusVersionFile } else { "" }
if ($installedPrometheus -ne $PrometheusVersion -or -Not (Test-Path (Join-Path $PrometheusHome "prometheus.exe"))) {
    $prometheusZip = Join-Path $Temp "$PrometheusDist.zip"
    Get-VerifiedFile `
      -Uri "https://github.com/prometheus/prometheus/releases/download/v$PrometheusVersion/$PrometheusDist.zip" `
      -OutFile $prometheusZip -Sha256 $PrometheusSha256
    $prometheusExtract = Join-Path $Temp "prometheus"
    Remove-Item -Recurse -Force $prometheusExtract -ErrorAction SilentlyContinue
    Expand-Archive -Path $prometheusZip -DestinationPath $prometheusExtract -Force
    # Stop first so prometheus.exe is not locked when it is overwritten.
    Stop-Service prometheus -ErrorAction SilentlyContinue
    New-Item -ItemType Directory -Force -Path $PrometheusHome | Out-Null
    Copy-Item -Force -Recurse (Join-Path $prometheusExtract "$PrometheusDist\*") $PrometheusHome
    Write-TextFile -Path $prometheusVersionFile -Content $PrometheusVersion
}

# Written after the release files, so it overwrites the sample config they ship.
$PrometheusConfig = @"
# Managed by the nvidia_gpu_exporter all-in-one install script; it is
# overwritten on every run of the script, so custom changes do not survive.
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
Write-TextFile -Path (Join-Path $PrometheusHome "prometheus.yml") -Content $PrometheusConfig

# Download WinSW once, self-healing a damaged copy on the next run. The x64
# build targets .NET Framework, which ships with Windows 10 and 11, so there is
# nothing else to install.
Get-VerifiedFile `
  -Uri "https://github.com/winsw/winsw/releases/download/$WinSWVersion/WinSW-x64.exe" `
  -OutFile $WinSW -Sha256 $WinSWSha256

$PrometheusServiceConfig = @"
<service>
  <id>prometheus</id>
  <name>Prometheus</name>
  <description>Prometheus monitoring server</description>
  <executable>$PrometheusHome\prometheus.exe</executable>
  <arguments>--config.file="$PrometheusHome\prometheus.yml" --storage.tsdb.path="$PrometheusHome\data"</arguments>
  <workingdirectory>$PrometheusHome</workingdirectory>
  <onfailure action="restart" delay="5 sec" />
  <log mode="roll" />
</service>
"@
Write-TextFile -Path (Join-Path $PrometheusHome "prometheus-service.xml") -Content $PrometheusServiceConfig
if (-Not (Get-Service prometheus -ErrorAction SilentlyContinue)) {
    & $WinSW install
    if ($LASTEXITCODE -ne 0) { throw "WinSW service install failed with exit code $LASTEXITCODE." }
}
# WinSW and Prometheus read their config at start, so an already running
# service is restarted to pick up config changes (starting a running service
# would be a silent no-op).
if ((Get-Service prometheus).Status -eq "Running") {
    Restart-Service prometheus
} else {
    Start-Service prometheus
}

# --- Grafana ------------------------------------------------------------------
# Grafana's MSI, installed via winget, registers its own "Grafana" Windows
# service, so it needs no wrapper. The datasource and the dashboards are
# provisioned into its conf\provisioning, so no manual import step is needed.

Write-Host "Installing Grafana..."
winget list --exact --id GrafanaLabs.Grafana.OSS --accept-source-agreements | Out-Null
if ($LASTEXITCODE -ne 0) {
    winget install --exact --id GrafanaLabs.Grafana.OSS --silent --accept-source-agreements --accept-package-agreements
    if ($LASTEXITCODE -ne 0) { throw "winget failed to install Grafana with exit code $LASTEXITCODE." }
}

# Locate the Grafana home from the service the MSI registered, so a customized
# install location is honored. The MSI wraps grafana.exe with a bundled copy of
# nssm, so the real binary is in the service's nssm parameters; fall back to the
# service path itself in case a future MSI registers the binary directly.
$grafanaService = Get-CimInstance Win32_Service -Filter "Name='Grafana'"
if (-Not $grafanaService) { throw "The Grafana MSI did not register the Grafana service." }
$grafanaExe = (Get-ItemProperty "HKLM:\SYSTEM\CurrentControlSet\Services\Grafana\Parameters" -ErrorAction SilentlyContinue).Application
if (-Not $grafanaExe -and $grafanaService.PathName -match '^"?(?<exe>[^"]+\\bin\\[^"\\]+\.exe)"?') {
    $grafanaExe = $Matches.exe
}
if ($grafanaExe -notlike "*\bin\*.exe") {
    throw "Could not determine the Grafana home from the Grafana service configuration."
}
$GrafanaHome = Split-Path (Split-Path $grafanaExe)

# Provision the Prometheus datasource as the default, so the dashboards'
# datasource variable resolves to it without any manual step.
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
New-Item -ItemType Directory -Force -Path "$GrafanaHome\conf\provisioning\datasources" | Out-Null
Write-TextFile `
  -Path "$GrafanaHome\conf\provisioning\datasources\prometheus.yaml" `
  -Content $GrafanaDataSourceConfig

# Provision both dashboards (per-GPU and multi-GPU overview) via a file
# provider.
$DashboardDir = "$GrafanaHome\conf\provisioning\dashboards\nvidia_gpu_exporter"
$GrafanaDashboardProvider = @"
apiVersion: 1
providers:
  - name: nvidia_gpu_exporter
    orgId: 1
    folder: ''
    type: file
    disableDeletion: false
    allowUiUpdates: true
    options:
      path: $DashboardDir
"@
Write-TextFile `
  -Path "$GrafanaHome\conf\provisioning\dashboards\nvidia_gpu_exporter.yaml" `
  -Content $GrafanaDashboardProvider
New-Item -ItemType Directory -Force -Path $DashboardDir | Out-Null
foreach ($name in @("dashboard.json", "dashboard-overview.json")) {
    Invoke-Download `
      -Uri "https://raw.githubusercontent.com/utkuozdemir/nvidia_gpu_exporter/main/docs/grafana/$name" `
      -OutFile (Join-Path $DashboardDir $name)
}

# Restart so a Grafana that was already running picks up the provisioning.
Restart-Service Grafana

Remove-Item -Recurse -Force $Temp -ErrorAction SilentlyContinue

Write-Host ""
Write-Host "All done. The exporter (v$version), Prometheus, and Grafana run as Windows services."
Write-Host "  Exporter metrics: http://localhost:9835/metrics"
Write-Host "  Prometheus:       http://localhost:9090"
Write-Host "  Grafana:          http://localhost:3000 (initial login: admin / admin)"
