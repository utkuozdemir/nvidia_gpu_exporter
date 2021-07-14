# Scoop
if (-Not(Get-Command "scoop" -errorAction SilentlyContinue))
{
    Invoke-Expression (New-Object System.Net.WebClient).DownloadString('https://get.scoop.sh') -ErrorAction SilentlyContinue
}
scoop bucket add extras

# NSSM
scoop install nssm --global

# Exporter
scoop bucket add nvidia_gpu_exporter https://github.com/utkuozdemir/scoop_nvidia_gpu_exporter.git
scoop install nvidia_gpu_exporter/nvidia_gpu_exporter --global
If (-Not(Get-Service "nvidia_gpu_exporter" -ErrorAction SilentlyContinue))
{
    nssm install nvidia_gpu_exporter "C:\ProgramData\scoop\apps\nvidia_gpu_exporter\current\nvidia_gpu_exporter.exe"
}
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
Set-Content -Path C:\ProgramData\scoop\apps\prometheus\current\prometheus.yml -Value $PrometheusConfig
If (-Not(Get-Service "prometheus" -ErrorAction SilentlyContinue))
{
    nssm install prometheus "C:\ProgramData\scoop\apps\prometheus\current\prometheus.exe"
}
Start-Service prometheus

# Grafana
$GrafanaDataSourceConfig = @"
apiVersion: 1
datasources:
  - name: Prometheus
    type: prometheus
    access: direct
    orgId: 1
    url: http://localhost:9090
    isDefault: true
    version: 1
    editable: false
"@
scoop install grafana --global
Invoke-WebRequest `
  -Uri https://raw.githubusercontent.com/utkuozdemir/nvidia_gpu_exporter/master/grafana/dashboard.json `
  -OutFile C:\ProgramData\scoop\apps\grafana\current\public\dashboards\nvidia_gpu_exporter.json
Set-Content `
  -Path C:\ProgramData\scoop\apps\grafana\current\conf\provisioning\datasources\prometheus.yaml `
  -Value $GrafanaDataSourceConfig
If (-Not(Get-Service "grafana" -ErrorAction SilentlyContinue))
{
    nssm install grafana "C:\ProgramData\scoop\apps\grafana\current\bin\grafana-server.exe"
}
Start-Service grafana
