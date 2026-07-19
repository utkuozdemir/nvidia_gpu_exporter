# nvidia-gpu-exporter

Nvidia GPU exporter for prometheus, using nvidia-smi binary to gather metrics.

The exporter runs as a DaemonSet. GPU access is injected by the NVIDIA
container runtime: the nodes need the NVIDIA driver and the
[NVIDIA Container Toolkit](https://docs.nvidia.com/datacenter/cloud-native/container-toolkit/latest/install-guide.html),
and the pods must run with the NVIDIA runtime. Either the runtime is the
default on your nodes, or you set `runtimeClassName` (usually to `nvidia`)
and make sure that RuntimeClass exists.

To try the experimental NVML backend, which reads the driver library
directly instead of running `nvidia-smi`, set the image tag to a `-nvml`
variant (for example `1.7.0-nvml`). It exports a superset of the default
backend's metrics: the same core set plus NVML-only extras like the GPU
energy counter, per-MIG-instance metrics, XID error counters and opt-in
PCIe throughput. The `-nvml` images are built for
linux/amd64 only, so on mixed-architecture clusters add
`kubernetes.io/arch: amd64` to `nodeSelector`.

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
GPU-level memory fields read `[Insufficient Permissions]`. By default
processes are attributed to the parent GPU's UUID, not to individual MIG
instances; on the `-nvml` image the attribution labels can be added with

```yaml
extraArgs:
  - --collect.compute-apps-mig
```

## Scheduling on GPU nodes

By default the DaemonSet runs on every Linux node, including nodes without a
GPU, where the pods come up but report `nvidia_smi_last_collect_success 0`.
On mixed clusters, restrict it to GPU nodes via `nodeSelector`. There is no
universal GPU node label, so use whatever your cluster has:

- `nvidia.com/gpu.present: "true"` with GPU Feature Discovery (installed by
  the NVIDIA GPU Operator, among others),
- `feature.node.kubernetes.io/pci-0302_10de.present: "true"` with plain Node
  Feature Discovery (the class segment varies by GPU model: `0302` for
  datacenter 3D controllers, `0300` for display-class cards, so check your
  node labels),
- a cloud or in-house label of your own, e.g. `cloud.google.com/gke-accelerator`
  on GKE (any value, so use `affinity` with an `Exists` match for that one).

GPU node pools are also commonly tainted (for example
`nvidia.com/gpu=present:NoSchedule`) so that only GPU workloads land on them.
The exporter deliberately requests no `nvidia.com/gpu` resource, so it does
not tolerate such taints by itself and will silently skip those nodes. Add a
matching toleration:

```yaml
nodeSelector:
  nvidia.com/gpu.present: "true"
tolerations:
  - key: nvidia.com/gpu
    operator: Exists
    effect: NoSchedule
```

## Running next to the NVIDIA GPU Operator

The GPU Operator and this chart coexist without conflicts. The Operator's
own dcgm-exporter listens on a different port (9400 vs 9835) and exports a
different metric namespace (`DCGM_FI_*` vs `nvidia_smi_*`), so nothing
clashes; running both just means the GPUs are polled twice. The Operator
also installs the pieces this chart needs anyway: the NVIDIA Container
Toolkit, a `nvidia` RuntimeClass to set `runtimeClassName` to, and GPU
Feature Discovery labels for the `nodeSelector` shown above.

To replace dcgm-exporter instead of running both, disable it with
`dcgmExporter.enabled=false` in the GPU Operator chart.

## Restricted namespaces

The pods run unprivileged, but the default security contexts are empty and
the image runs as root, which the `restricted`
[Pod Security Standard](https://kubernetes.io/docs/concepts/security/pod-security-standards/)
rejects at admission. GPU access via the NVIDIA runtime does not require
root (the injected device nodes are world-accessible on standard driver
installs), so in enforcing namespaces set a compliant security context:

```yaml
securityContext:
  runAsNonRoot: true
  runAsUser: 65534
  allowPrivilegeEscalation: false
  capabilities:
    drop:
      - ALL
  seccompProfile:
    type: RuntimeDefault
```

Note that `hostNetwork` and `hostPort` are rejected in such namespaces no
matter the security context (the `baseline` level already forbids them), and
`computeApps` needs `hostPID`, which they forbid too. On OpenShift, leave
`runAsUser` unset and let the namespace SCC assign one; the default
`restricted-v2` SCC fits this workload.

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
`prometheusRule` adds alerts on the exporter's collection health and on GPU
health: driver-requested recovery actions, critical Xid errors, uncorrectable
ECC errors, memory row-remap and page-retirement state, hardware thermal
slowdown and power braking. Every alert's description says what to do about
it. The defaults are deliberately conservative for consumer and homelab
hardware; rules that depend on fleet knowledge (absolute temperature, GPU
count) or that are normal on thermally-limited systems (software thermal
slowdown) ship disabled, and each rule can be toggled individually. Some
rules need the NVML backend (Xid) or specific query fields (ECC, remapped
rows); where the metric is absent the rule simply never fires. When
upgrading a release that already had `prometheusRule.enabled: true`, the new
GPU health rules arrive enabled: review the list below and disable any you
do not want. One real
hardware fault can raise several of these alerts at once (for example an
uncorrectable ECC error also produces an Xid and a recovery action); that
overlap is deliberate, because no single signal exists on every backend,
driver and GPU generation combination. Group them in your Alertmanager route
by `instance` if you prefer one notification per node.

If the exporter also runs on nodes without GPUs, restrict the DaemonSet to
GPU nodes via `nodeSelector` or `affinity` before enabling `prometheusRule`,
otherwise the collection alerts fire for nodes that cannot collect GPU
metrics by design. The alert expressions select all `nvidia_smi_*` series, so
when installing multiple releases of this chart, enable the rules in only one
of them.
`grafanaDashboard` ships the [Grafana dashboard](https://grafana.com/grafana/dashboards/14574)
and its [multi-GPU overview companion](https://github.com/utkuozdemir/nvidia_gpu_exporter/blob/main/docs/grafana/dashboard-overview.json)
as a ConfigMap labeled for the Grafana sidecar.

With [kube-prometheus-stack](https://github.com/prometheus-community/helm-charts/tree/main/charts/kube-prometheus-stack),
enabling a monitor is usually not enough: by default its Prometheus only
selects monitors carrying the stack's release label. Two more things bite in
practice: the default `instance` label is the pod IP, which changes on every
restart and splits the per-GPU series, so relabel it to the node name. And
the Grafana sidecar only reads dashboard ConfigMaps from other namespaces
when it runs with `sidecar.dashboards.searchNamespace=ALL`.

```yaml
serviceMonitor:
  enabled: true
  additionalLabels:
    release: kube-prometheus-stack # your stack's release name
  relabelings:
    - sourceLabels: [__meta_kubernetes_pod_node_name]
      targetLabel: instance
prometheusRule:
  enabled: true
  additionalLabels:
    release: kube-prometheus-stack
grafanaDashboard:
  enabled: true
```

## Values

| Key | Type | Default | Description |
|-----|------|---------|-------------|
| affinity | object | `{}` | Affinity for the pods |
| automountServiceAccountToken | bool | `false` | Mount the service account token into the pods. The exporter never talks to the Kubernetes API, so it is off by default. Enable it only if something injected into the pods (e.g. a service mesh sidecar) needs the token. |
| computeApps.enabled | bool | `false` | Also export per-process GPU metrics (`nvidia_smi_compute_app_*`). To see processes of other pods and containers, the exporter must share the host PID namespace: enable `hostPID` along with this. Note that the pid label churns with the processes, creating short-lived series. |
| extraArgs | list | `[]` | Extra command line arguments for the exporter, e.g. `--collect.interval=30s` |
| extraEnv | list | `[]` | Extra environment variables for the exporter container |
| fullnameOverride | string | `""` | Override the fully qualified app name |
| grafanaDashboard.annotations | object | `{}` | Annotations for the dashboard ConfigMap, e.g. the folder annotation of the sidecar |
| grafanaDashboard.enabled | bool | `false` | Create a ConfigMap with the Grafana dashboards (single-GPU detail and multi-GPU overview), labeled for the Grafana sidecar to pick up |
| grafanaDashboard.label | string | `"grafana_dashboard"` | Label that the Grafana sidecar watches for |
| grafanaDashboard.labelValue | string | `"1"` | Value of the sidecar label |
| hostNetwork | bool | `false` | Use the host network for the pods. Also switches their DNS policy to `ClusterFirstWithHostNet` so that cluster DNS keeps working. |
| hostPID | bool | `false` | Share the host PID namespace with the pods. Required for computeApps to see processes of other pods and containers, but it also lets the exporter pod see all host process names, which some security policies forbid. |
| hostPort.enabled | bool | `false` | Expose the metrics port on the host |
| hostPort.port | int | `9835` | The host port to expose the metrics on |
| image.pullPolicy | string | `"IfNotPresent"` | Image pull policy |
| image.repository | string | `"docker.io/utkuozdemir/nvidia_gpu_exporter"` | Image repository |
| image.tag | string | `""` | Image tag (if not specified, defaults to the chart's appVersion) |
| imagePullSecrets | list | `[]` | Image pull secrets, used by both the exporter and the `helm test` pods |
| livenessProbe | object | `{"httpGet":{"path":"/-/healthy","port":"http"}}` | Liveness probe for the exporter container. The default checks that the process serves HTTP at all; it deliberately does not depend on collection success, so a failing nvidia-smi keeps the pod scrapeable and the failure visible in the metrics. Set to `null` to disable the probe. |
| log.format | string | `"logfmt"` | Log format: logfmt, json |
| log.level | string | `"info"` | Log level: debug, info, warn, error |
| nameOverride | string | `""` | Override the chart name |
| nodeSelector | object | `{"kubernetes.io/os":"linux"}` | Node selector for the pods. The images are Linux-only, hence the default. Add a GPU node label to keep the DaemonSet off non-GPU nodes, e.g. `nvidia.com/gpu.present: "true"` on clusters with GPU Feature Discovery (see the README section on scheduling). Helm merges maps key by key, so overriding with `{}` does not clear the default; set the whole value to `null` to remove it. |
| nvidiaDriverCapabilities | string | `"utility"` | NVIDIA driver capability tier to request. `utility` is the nvidia-smi/NVML tier, which is all the exporter needs. |
| nvidiaSmiCommand | string | `"nvidia-smi"` | The command to run to get `nvidia-smi` compatible output. Can be a custom path and/or args. |
| nvidiaVisibleDevices | string | `"all"` | Which GPUs to make visible to the exporter. `all` monitors every GPU on the node. |
| podAnnotations | object | `{}` | Annotations to add to the pods |
| podLabels | object | `{}` | Extra labels to add to the pods |
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
| prometheusRule.collectionSlow.enabled | bool | `true` | Alert when collecting GPU metrics is slow for 10 minutes. Healthy nvidia-smi answers well under a second with the persistence daemon running; sustained slowness often precedes a wedged driver. |
| prometheusRule.collectionSlow.thresholdSeconds | int | `5` | Collection duration in seconds above which the alert fires |
| prometheusRule.collectionStale.enabled | bool | `true` | Alert when the last successful collection is too far in the past |
| prometheusRule.collectionStale.severity | string | `"warning"` | Severity label of the alert |
| prometheusRule.collectionStale.thresholdSeconds | int | `300` | Seconds since the last successful collection before the alert fires |
| prometheusRule.enabled | bool | `false` | Create a Prometheus Operator PrometheusRule with default alerts (requires the Prometheus Operator CRDs). In clusters where the exporter also runs on nodes without GPUs, restrict the DaemonSet to GPU nodes via nodeSelector or affinity before enabling this, otherwise the alerts fire for nodes that cannot collect GPU metrics by design. |
| prometheusRule.gpuMissing.enabled | bool | `false` | Alert when a machine reports fewer GPUs than its recent maximum while collection stays healthy, which catches GPUs that silently fall off the bus on the default backend. Off by default: it also fires for up to 6 hours after deliberately removing a GPU and needs a stable instance label across exporter restarts. |
| prometheusRule.powerBrake.enabled | bool | `true` | Alert when an external hardware power brake throttles a GPU for 5 minutes: a power delivery problem (PSU, cables, rack power). The configured software power limit never raises this. |
| prometheusRule.recoveryAction.enabled | bool | `true` | Alert when the driver itself requests a GPU recovery action (reset, reboot, drain). The most direct health signal there is; needs a recent driver (570+), silently absent on older ones. |
| prometheusRule.retiredPagesPending.enabled | bool | `true` | Alert when a GPU memory page retirement waits for a reboot to take effect (pre-Ampere GPUs) |
| prometheusRule.rowRemapFailure.enabled | bool | `true` | Alert when GPU memory row remapping failed, which is NVIDIA's stated RMA criterion |
| prometheusRule.rowRemapPending.enabled | bool | `true` | Alert when a GPU memory row remap waits for a GPU reset to take effect |
| prometheusRule.swThermalSlowdown.enabled | bool | `false` | Alert when the driver throttles a GPU at its temperature target for 30 minutes. Off by default: this is normal on thermally-limited consumer and laptop systems under sustained load. |
| prometheusRule.temperatureHigh.enabled | bool | `false` | Alert when the GPU core temperature stays above the threshold for 10 minutes. Off by default: safe temperatures differ per GPU generation (a datacenter A100 throttles around 83C while laptop GPUs sit at 87C by design), so tune the threshold to your fleet before enabling. |
| prometheusRule.temperatureHigh.thresholdCelsius | int | `85` | Core temperature in Celsius above which the alert fires |
| prometheusRule.thermalSlowdown.enabled | bool | `true` | Alert when hardware thermal protection throttles a GPU for 5 minutes: cooling is failing at the hardware trip point. The driver applies the right per-model threshold, so this works across GPU generations. Disable if it fires routinely on thermally-limited mobile/small-form-factor systems. |
| prometheusRule.uncorrectableEcc.enabled | bool | `true` | Alert when a GPU reports uncorrectable ECC memory errors since its last reset. Data may have been corrupted; a GPU reset repairs the memory on Ampere and newer. Corrected (single-bit) errors never alert. |
| prometheusRule.xidCritical.codes | list | `[48,62,64,74,79,95,119,120]` | Xid codes treated as critical (reset/reboot class) |
| prometheusRule.xidCritical.enabled | bool | `true` | Alert on Xid errors whose NVIDIA-recommended action is a GPU reset or node reboot. Needs the NVML backend; never fires on the default nvidia-smi backend. |
| prometheusRule.xidWarning.codes | list | `[94]` | Xid codes treated as warning (restart-app class) |
| prometheusRule.xidWarning.enabled | bool | `true` | Alert on Xid errors whose recommended action is an application restart. Explicit allowlist: application-caused Xids (13, 31, 43, ...) and informational ones (63, 92) deliberately never alert. |
| queryFieldNames | list | `["AUTO"]` | `nvidia-smi` fields to be queried by the exporter. `AUTO` auto-detects them. |
| queryFieldNamesExclude | list | `[]` | `nvidia-smi` fields to exclude from being queried. Names match literally, with `*` as a wildcard for any sequence of characters. |
| readinessProbe | object | `{"httpGet":{"path":"/-/ready","port":"http"}}` | Readiness probe for the exporter container, process-level like the liveness probe. Set to `null` to disable the probe. |
| resources | object | `{}` | Resources for the exporter container |
| revisionHistoryLimit | string | `""` | How many old DaemonSet history revisions to retain for rollbacks. Empty means the Kubernetes default (10). |
| runtimeClassName | string | `""` | Name of the RuntimeClass to run the pods with. GPU access is injected by the NVIDIA container runtime, so the pods must run with it: either set this to the name of your NVIDIA RuntimeClass (usually `nvidia`), or leave it empty if the NVIDIA runtime is the default runtime of your nodes. If neither is the case, the exporter will come up but serve no GPU metrics, reporting `nvidia_smi_last_collect_success 0`. |
| securityContext | object | `{}` | Security context for the exporter container. The default is unprivileged: GPU access comes from the NVIDIA runtime, which requires no privileges. |
| service.annotations | object | `{}` | Annotations to add to the Service |
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
| test.image.pullPolicy | string | `"IfNotPresent"` | Image pull policy for the `helm test` pod |
| test.image.repository | string | `"docker.io/library/busybox"` | Image repository for the `helm test` connection-check pod |
| test.image.tag | string | `"1.38"` | Image tag for the `helm test` pod |
| tolerations | list | `[]` | Tolerations for the pods. GPU node pools are often tainted (e.g. `nvidia.com/gpu=present:NoSchedule`) so that only GPU workloads land on them. This chart deliberately requests no GPU resource, so add a matching toleration here or the exporter will not schedule on those nodes. |
| updateStrategy | object | `{"rollingUpdate":{"maxUnavailable":1},"type":"RollingUpdate"}` | Update strategy of the DaemonSet. Raise `rollingUpdate.maxUnavailable` (absolute or percentage) to roll out faster on large clusters, or use `type: OnDelete` for manually staged rollouts. |
| volumeMounts | list | `[]` | Extra volume mounts for the exporter container |
| volumes | list | `[]` | Extra volumes for the pods |

----------------------------------------------
Autogenerated from chart metadata using [helm-docs v1.14.2](https://github.com/norwoodj/helm-docs/releases/v1.14.2)
