# Uninstalls the all-in-one setup: the exporter, Prometheus and Grafana Windows
# services and program files, whether they were set up by the current install
# script (native service, WinSW wrapper, winget MSI) or by its old scoop- and
# nssm-based version. Collected data is never deleted: the Prometheus time
# series and the Grafana state stay on disk, and their locations are printed at
# the end. A service whose binary does not belong to this setup is left alone.
#
# Run this from an elevated PowerShell prompt (Run as administrator).

$ErrorActionPreference = "Stop"

$isAdmin = ([Security.Principal.WindowsPrincipal][Security.Principal.WindowsIdentity]::GetCurrent()).IsInRole([Security.Principal.WindowsBuiltinRole]::Administrator)
if (-Not $isAdmin) {
    throw "This script must be run from an elevated PowerShell prompt (Run as administrator)."
}

$ExporterHome = "$env:ProgramFiles\nvidia_gpu_exporter"
$PrometheusHome = "$env:ProgramFiles\Prometheus"

# Paths that identify a service as belonging to this setup, old or new.
$OwnedPathFragments = @(
    "\scoop\",                        # old: scoop payloads and the nssm wrapper
    "\nvidia_gpu_exporter\services\", # old: an interim wrapped-services layout
    "$ExporterHome\",                 # new: the native exporter service
    "$PrometheusHome\"                # new: the WinSW-wrapped Prometheus
)

# Stop and delete a service, but only when its binary identifies it as part of
# this setup, so an unrelated service that happens to share the name survives.
function Remove-OwnedService {
    param(
        [string] $Id
    )
    if (-Not (Get-Service $Id -ErrorAction SilentlyContinue)) { return }
    $path = (Get-CimInstance Win32_Service -Filter "Name='$Id'").PathName
    $owned = $false
    foreach ($fragment in $OwnedPathFragments) {
        if ($path -like "*$fragment*") { $owned = $true }
    }
    if (-Not $owned) {
        Write-Host "Skipping the '$Id' service: its binary ($path) does not belong to this setup."
        return
    }
    Stop-Service $Id -Force -ErrorAction SilentlyContinue
    & sc.exe delete $Id | Out-Null
    if ($LASTEXITCODE -ne 0) { throw "Failed to delete the '$Id' service (sc.exe exit code $LASTEXITCODE)." }
    $tries = 0
    while ((Get-Service $Id -ErrorAction SilentlyContinue) -and $tries -lt 30) {
        Start-Sleep -Milliseconds 300
        $tries++
    }
    if (Get-Service $Id -ErrorAction SilentlyContinue) {
        throw "The '$Id' service is still present after deletion. Something may be holding a handle to it (an open services console, for example); close it and re-run."
    }
    Write-Host "Removed the '$Id' service."
}

# Exporter: the exe's own uninstall subcommand also removes its event log
# source, so prefer it when the new-layout exe is what the service runs.
$ExporterExe = Join-Path $ExporterHome "nvidia_gpu_exporter.exe"
$exporterService = Get-CimInstance Win32_Service -Filter "Name='nvidia_gpu_exporter'"
if ($exporterService -and ($exporterService.PathName -like "*$ExporterExe*") -and (Test-Path $ExporterExe)) {
    Stop-Service nvidia_gpu_exporter -Force -ErrorAction SilentlyContinue
    & $ExporterExe uninstall
    if ($LASTEXITCODE -ne 0) { throw "nvidia_gpu_exporter uninstall failed with exit code $LASTEXITCODE." }
    Write-Host "Removed the 'nvidia_gpu_exporter' service."
} else {
    Remove-OwnedService -Id "nvidia_gpu_exporter"
}

Remove-OwnedService -Id "prometheus"

# Grafana: when the winget MSI owns the service, uninstall the package (which
# removes the service too); a wrapped service from the old script is deleted
# directly.
$grafanaService = Get-CimInstance Win32_Service -Filter "Name='grafana'"
if ($grafanaService -and $grafanaService.PathName -like "*GrafanaLabs*") {
    if (-Not (Get-Command winget -ErrorAction SilentlyContinue)) {
        throw "winget is required to uninstall the Grafana package but was not found."
    }
    winget uninstall --exact --id GrafanaLabs.Grafana.OSS --silent --disable-interactivity
    if ($LASTEXITCODE -ne 0) { throw "winget failed to uninstall Grafana with exit code $LASTEXITCODE." }
    Write-Host "Uninstalled the Grafana package."
} else {
    Remove-OwnedService -Id "grafana"
}

# Program files: the exporter directory holds no data, so it goes entirely.
# The Prometheus directory is deleted around its data subdirectory, which is
# kept in place.
Remove-Item -Recurse -Force $ExporterHome -ErrorAction SilentlyContinue
if (Test-Path "$PrometheusHome\data") {
    Get-ChildItem -Force $PrometheusHome | Where-Object { $_.Name -ne "data" } | Remove-Item -Recurse -Force
} else {
    Remove-Item -Recurse -Force $PrometheusHome -ErrorAction SilentlyContinue
}

Write-Host ""
Write-Host "Uninstall complete. Data and leftovers kept on disk (delete them yourself if unwanted):"
foreach ($kept in @(
    "$PrometheusHome\data",                          # new: Prometheus time series
    "$env:ProgramFiles\GrafanaLabs",                 # new: Grafana state the MSI leaves behind
    "C:\ProgramData\scoop\persist\prometheus",       # old: scoop-persisted Prometheus data
    "C:\ProgramData\scoop\persist\grafana",          # old: scoop-persisted Grafana state
    "C:\ProgramData\scoop\apps"                      # old: scoop-installed payloads
)) {
    if (Test-Path $kept) { Write-Host "  $kept" }
}
