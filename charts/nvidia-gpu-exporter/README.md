# nvidia-gpu-exporter

Nvidia GPU exporter for prometheus, using nvidia-smi binary to gather metrics.

The exporter runs as a DaemonSet. GPU access is injected by the NVIDIA
container runtime: the nodes need the NVIDIA driver and the
[NVIDIA Container Toolkit](https://docs.nvidia.com/datacenter/cloud-native/container-toolkit/latest/install-guide.html),
and the pods must run with the NVIDIA runtime. Either the runtime is the
default on your nodes, or you set `runtimeClassName` (usually to `nvidia`)
and make sure that RuntimeClass exists.

The exporter deliberately requests no `nvidia.com/gpu` resource. The device
plugin allocates whole GPUs exclusively, so a monitoring pod that requested
one would take that GPU away from real workloads. The runtime environment
variable approach gives visibility of all GPUs on the node without reserving
any of them.

## Installing

```bash
helm install nvidia-gpu-exporter oci://ghcr.io/utkuozdemir/charts/nvidia-gpu-exporter \
  --set runtimeClassName=nvidia
```

Or from the classic repository:

```bash
helm repo add nvidia-gpu-exporter https://utkuozdemir.github.io/nvidia_gpu_exporter
helm install nvidia-gpu-exporter nvidia-gpu-exporter/nvidia-gpu-exporter \
  --set runtimeClassName=nvidia
```

### Verifying the chart signature

Releases from the classic repository are signed with GPG provenance files
(key fingerprint `93122B2C53431C2F60964EB7EAC49314A32B9205`). To verify,
fetch the public key once and pass it to helm:

```bash
curl -fsSL https://utkuozdemir.github.io/nvidia_gpu_exporter/pubkey.asc | gpg --dearmor > nvidia-gpu-exporter.gpg
helm install nvidia-gpu-exporter nvidia-gpu-exporter/nvidia-gpu-exporter \
  --verify --keyring nvidia-gpu-exporter.gpg \
  --set runtimeClassName=nvidia
```

Helm's provenance check only covers classic-repository installs. The OCI
artifact on GHCR is signed separately with cosign (keyless), so verify an OCI
install like this (replace `CHART_VERSION` with the version you are pulling):

```bash
cosign verify \
  --certificate-oidc-issuer=https://token.actions.githubusercontent.com \
  --certificate-identity-regexp='^https://github\.com/utkuozdemir/nvidia_gpu_exporter/\.github/workflows/release\.yml@refs/tags/v.*$' \
  ghcr.io/utkuozdemir/charts/nvidia-gpu-exporter:CHART_VERSION
```

## Per-process GPU metrics

Enable `computeApps.enabled` to also export per-process GPU metrics
(`nvidia_smi_compute_app_*`). Process visibility follows the PID namespace:
without `hostPID` the exporter only sees GPU processes visible inside its own
container, so it normally will not report processes from other pods or
containers. Enable `hostPID` along with it. Note that `hostPID` lets the
exporter pod see all host process names, which some security policies forbid.

```bash
helm upgrade nvidia-gpu-exporter oci://ghcr.io/utkuozdemir/charts/nvidia-gpu-exporter \
  --set runtimeClassName=nvidia \
  --set computeApps.enabled=true \
  --set hostPID=true
```

On MIG-enabled GPUs the requirements are steeper: the exporter container must
run privileged with the `NVIDIA_MIG_MONITOR_DEVICES=all` environment variable
(via `securityContext` and `extraEnv`) on top of `hostPID`, otherwise even
GPU-level memory fields read `[Insufficient Permissions]`. Processes are
attributed to the parent GPU's UUID, not to individual MIG instances.

## Upgrading from chart 1.x

Chart 1.x lived in a [separate repository](https://github.com/utkuozdemir/helm-charts)
and mounted GPU device files, the `nvidia-smi` binary, and driver libraries
from the host into the pod, running privileged. That approach broke on driver
upgrades and non-x86 nodes. Starting with chart 2.x, GPU access comes from
the NVIDIA container runtime and the pod runs unprivileged.

What to do when upgrading:

- Make sure the nodes have the NVIDIA Container Toolkit and set
  `runtimeClassName` as described above.
- Drop any custom `volumes`, `volumeMounts`, or `securityContext` values you
  carried for the old hand-mounted setup.
- The `ingress` values were removed. Use your own Ingress resource if you
  need one, though for an internal metrics endpoint you usually do not.
- The chart version scheme changed: the chart major is the app major plus
  one, so chart `2.8.x` ships app `1.8.x`.

## Monitoring extras

All optional resources are off by default. `serviceMonitor` and `podMonitor`
require the Prometheus Operator CRDs (enable one of them, not both).
`prometheusRule` adds alerts on the exporter's collection health metrics: if
the exporter also runs on nodes without GPUs, restrict the DaemonSet to GPU
nodes via `nodeSelector` or `affinity` before enabling it, otherwise the
alerts fire for nodes that cannot collect GPU metrics by design.
`grafanaDashboard` ships the [Grafana dashboard](https://grafana.com/grafana/dashboards/14574)
as a ConfigMap labeled for the Grafana sidecar.

## Values

| Key | Type | Default | Description |
|-----|------|---------|-------------|
| affinity | object | `{}` | Affinity for the pods |
| computeApps.enabled | bool | `false` | Also export per-process GPU metrics (`nvidia_smi_compute_app_*`). To see processes of other pods and containers, the exporter must share the host PID namespace: enable `hostPID` along with this. Note that the pid label churns with the processes, creating short-lived series. |
| extraArgs | list | `[]` | Extra command line arguments for the exporter, e.g. `--collect.interval=30s` |
| extraEnv | list | `[]` | Extra environment variables for the exporter container |
| fullnameOverride | string | `""` | Override the fully qualified app name |
| grafanaDashboard.annotations | object | `{}` | Annotations for the dashboard ConfigMap, e.g. the folder annotation of the sidecar |
| grafanaDashboard.enabled | bool | `false` | Create a ConfigMap with the Grafana dashboard, labeled for the Grafana sidecar to pick up |
| grafanaDashboard.label | string | `"grafana_dashboard"` | Label that the Grafana sidecar watches for |
| grafanaDashboard.labelValue | string | `"1"` | Value of the sidecar label |
| hostNetwork | bool | `false` | Use the host network for the pods |
| hostPID | bool | `false` | Share the host PID namespace with the pods. Required for computeApps to see processes of other pods and containers, but it also lets the exporter pod see all host process names, which some security policies forbid. |
| hostPort.enabled | bool | `false` | Expose the metrics port on the host |
| hostPort.port | int | `9835` | The host port to expose the metrics on |
| image.pullPolicy | string | `"IfNotPresent"` | Image pull policy |
| image.repository | string | `"docker.io/utkuozdemir/nvidia_gpu_exporter"` | Image repository |
| image.tag | string | `""` | Image tag (if not specified, defaults to the chart's appVersion) |
| imagePullSecrets | list | `[]` | Image pull secrets |
| log.format | string | `"logfmt"` | Log format: logfmt, json |
| log.level | string | `"info"` | Log level: debug, info, warn, error |
| nameOverride | string | `""` | Override the chart name |
| nodeSelector | object | `{}` | Node selector for the pods, e.g. to restrict the DaemonSet to GPU nodes |
| nvidiaDriverCapabilities | string | `"utility"` | NVIDIA driver capability tier to request. `utility` is the nvidia-smi/NVML tier, which is all the exporter needs. |
| nvidiaSmiCommand | string | `"nvidia-smi"` | The command to run to get `nvidia-smi` compatible output. Can be a custom path and/or args. |
| nvidiaVisibleDevices | string | `"all"` | Which GPUs to make visible to the exporter. `all` monitors every GPU on the node. |
| podAnnotations | object | `{}` | Annotations to add to the pods |
| podMonitor.additionalLabels | object | `{}` | Additional labels for the PodMonitor |
| podMonitor.enabled | bool | `false` | Create a Prometheus Operator PodMonitor instead of a ServiceMonitor (requires the Prometheus Operator CRDs). Enable either this or the ServiceMonitor, not both, otherwise the targets are scraped twice. |
| podMonitor.interval | string | `"15s"` | Scrape interval |
| podMonitor.metricRelabelings | list | `[]` | Relabelings to apply to the scraped metrics |
| podMonitor.relabelings | list | `[]` | Relabelings to apply to the scraped targets |
| podMonitor.scrapeTimeout | string | `""` | Scrape timeout |
| podSecurityContext | object | `{}` | Security context for the pods |
| port | int | `9835` | Port to listen on |
| priorityClassName | string | `""` | Priority class name for the pods |
| prometheusRule.additionalLabels | object | `{}` | Additional labels for the PrometheusRule, e.g. to match your Prometheus instance's rule selector |
| prometheusRule.collectionFailing.enabled | bool | `true` | Alert when the most recent collection failed for some time |
| prometheusRule.collectionFailing.for | string | `"10m"` | How long collection must be failing before the alert fires |
| prometheusRule.collectionFailing.severity | string | `"warning"` | Severity label of the alert |
| prometheusRule.collectionStale.enabled | bool | `true` | Alert when the last successful collection is too far in the past |
| prometheusRule.collectionStale.severity | string | `"warning"` | Severity label of the alert |
| prometheusRule.collectionStale.thresholdSeconds | int | `300` | Seconds since the last successful collection before the alert fires |
| prometheusRule.enabled | bool | `false` | Create a Prometheus Operator PrometheusRule with default alerts (requires the Prometheus Operator CRDs). In clusters where the exporter also runs on nodes without GPUs, restrict the DaemonSet to GPU nodes via nodeSelector or affinity before enabling this, otherwise the alerts fire for nodes that cannot collect GPU metrics by design. |
| queryFieldNames | list | `["AUTO"]` | `nvidia-smi` fields to be queried by the exporter. `AUTO` auto-detects them. |
| queryFieldNamesExclude | list | `[]` | `nvidia-smi` fields to exclude from being queried. Names match literally, with `*` as a wildcard for any sequence of characters. |
| resources | object | `{}` | Resources for the exporter container |
| runtimeClassName | string | `""` | Name of the RuntimeClass to run the pods with. GPU access is injected by the NVIDIA container runtime, so the pods must run with it: either set this to the name of your NVIDIA RuntimeClass (usually `nvidia`), or leave it empty if the NVIDIA runtime is the default runtime of your nodes. If neither is the case, the exporter will come up but serve no GPU metrics, reporting `nvidia_smi_last_collect_success 0`. |
| securityContext | object | `{}` | Security context for the exporter container. The default is unprivileged: GPU access comes from the NVIDIA runtime, which requires no privileges. |
| service.enabled | bool | `true` | Create a Service for the exporter |
| service.nodePort | string | `""` | Node port to use for NodePort/LoadBalancer service types |
| service.port | int | `9835` | Service port |
| service.type | string | `"ClusterIP"` | Service type |
| serviceAccount.annotations | object | `{}` | Annotations to add to the service account |
| serviceAccount.create | bool | `true` | Create a service account for the exporter |
| serviceAccount.name | string | `""` | The name of the service account to use. If not set and create is true, a name is generated. |
| serviceMonitor.additionalLabels | object | `{}` | Additional labels for the ServiceMonitor, e.g. to match your Prometheus instance's selector |
| serviceMonitor.bearerTokenFile | string | `""` | Bearer token file for scraping |
| serviceMonitor.enabled | bool | `false` | Create a Prometheus Operator ServiceMonitor (requires the Prometheus Operator CRDs) |
| serviceMonitor.interval | string | `"15s"` | Scrape interval |
| serviceMonitor.metricRelabelings | list | `[]` | Relabelings to apply to the scraped metrics |
| serviceMonitor.relabelings | list | `[]` | Relabelings to apply to the scraped targets |
| serviceMonitor.scheme | string | `"http"` | Scrape scheme |
| serviceMonitor.scrapeTimeout | string | `""` | Scrape timeout |
| serviceMonitor.tlsConfig | object | `{}` | TLS configuration for scraping |
| telemetryPath | string | `"/metrics"` | The path to expose the metrics from |
| tolerations | list | `[]` | Tolerations for the pods |
| volumeMounts | list | `[]` | Extra volume mounts for the exporter container |
| volumes | list | `[]` | Extra volumes for the pods |

----------------------------------------------
Autogenerated from chart metadata using [helm-docs v1.14.2](https://github.com/norwoodj/helm-docs/releases/v1.14.2)
