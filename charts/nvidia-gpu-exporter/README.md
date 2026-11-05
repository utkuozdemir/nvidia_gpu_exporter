# nvidia-gpu-exporter

Prometheus exporter for NVIDIA GPUs.

The exporter runs as a DaemonSet. GPU access is injected by the NVIDIA
container runtime: the nodes need the NVIDIA driver and the
[NVIDIA Container Toolkit](https://docs.nvidia.com/datacenter/cloud-native/container-toolkit/latest/install-guide.html),
and the pods must run with the NVIDIA runtime. Either the runtime is the
default on your nodes, or you set `runtimeClassName` (usually to `nvidia`)
and make sure that RuntimeClass exists. On clusters that use
[Dynamic Resource Allocation (DRA)](#dynamic-resource-allocation-dra) instead
of an `nvidia` `RuntimeClass`, leave `runtimeClassName` empty and pass
`resourceClaims` and `resources.claims`.

We recommend the native NVML image variant (`X.Y.Z-nvml`), which reads
`libnvidia-ml.so.1` in-process instead of spawning `nvidia-smi` on every
scrape. It exports the same core metrics as the default image, plus NVML-only
families: the GPU energy counter, per-MIG-instance metrics, hardware GPM
activity, XID error counters, PCIe replay counters, NVLink byte counters,
per-process SM/memory/codec utilization, and opt-in PCIe throughput (see the
[metrics reference](https://github.com/utkuozdemir/nvidia_gpu_exporter/blob/main/docs/METRICS.md#nvml-only-metrics)).
When `image.tag` is left empty, the chart defaults to the `exec`
(`nvidia-smi`) image for 1.x compatibility.

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

To run the recommended native NVML backend, set `image.tag` to the release's
`-nvml` tag (`--set image.tag=X.Y.Z-nvml`). Because setting `image.tag`
overrides the chart's default `appVersion`, update `image.tag` when upgrading
the chart.

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
artifact on GHCR is signed separately with cosign (keyless):

```bash
cosign verify \
  --certificate-oidc-issuer=https://token.actions.githubusercontent.com \
  --certificate-identity-regexp='^https://github\.com/utkuozdemir/nvidia_gpu_exporter/\.github/workflows/release\.yml@refs/tags/v.*$' \
  ghcr.io/utkuozdemir/charts/nvidia-gpu-exporter:CHART_VERSION
```

## Per-process GPU metrics

Enable `computeApps.enabled` to also export per-process GPU metrics
(`nvidia_smi_compute_app_*`). Process visibility follows the PID namespace:
without `hostPID` the exporter only sees GPU processes inside its own
container, so enable `hostPID` along with it. Note that `hostPID` lets the
exporter pod see all host process names, which some security policies forbid.

```bash
helm upgrade nvidia-gpu-exporter oci://ghcr.io/utkuozdemir/charts/nvidia-gpu-exporter \
  --set runtimeClassName=nvidia \
  --set computeApps.enabled=true \
  --set hostPID=true
```

On MIG-enabled GPUs, the exporter container also needs the
`NVIDIA_MIG_MONITOR_DEVICES=all` environment variable and privileged mode,
otherwise the GPU-level memory fields read `[Insufficient Permissions]`. Root
is not needed: the driver's MIG monitor device is world-readable by default,
so the exporter keeps running as uid 65534 (checked on an H200, including
per-process MIG attribution).

Use the whole block below. Your values merge onto the chart defaults key by
key, so setting `privileged` on its own conflicts with the default
`allowPrivilegeEscalation: false`, and the chart refuses to render it.

```yaml
hostPID: true
extraEnv:
  - name: NVIDIA_MIG_MONITOR_DEVICES
    value: all
securityContext:
  privileged: true
  allowPrivilegeEscalation: true
  capabilities: null
```

Without privileged mode (just the environment variable), the per-MIG-instance
inventory, memory and activity metrics still work, and only the GPU-level
memory fields go absent.

By default processes are attributed to the parent GPU's UUID, not to individual
MIG instances. On the `-nvml` image, per-instance attribution labels can be
added with:

```yaml
extraArgs:
  - --collect.compute-apps-mig
```

## Scheduling on GPU nodes

By default the DaemonSet runs on every Linux node. On nodes without a GPU, the
default `exec` image comes up and reports `nvidia_smi_last_collect_success 0`,
while the `-nvml` image exits at startup because `libnvidia-ml.so.1` is not
injected. On mixed clusters, restrict the DaemonSet to GPU nodes via
`nodeSelector` using whatever GPU label your cluster has:

- `nvidia.com/gpu.present: "true"` with GPU Feature Discovery (installed by
  the NVIDIA GPU Operator, among others)
- `feature.node.kubernetes.io/pci-0302_10de.present: "true"` with plain Node
  Feature Discovery (`0302` for datacenter 3D controllers, `0300` for
  display-class cards)
- a cloud or in-house label of your own, such as
  `cloud.google.com/gke-accelerator` on GKE (use `affinity` with an `Exists`
  match when the label value varies)

GPU node pools are also commonly tainted (for example
`nvidia.com/gpu=present:NoSchedule`). Because the exporter requests no
`nvidia.com/gpu` resource, it does not tolerate such taints automatically.
Add a matching toleration:

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
`dcgm-exporter` listens on port 9400 and exports `DCGM_FI_*` metrics, while
this exporter listens on port 9835 and exports `nvidia_smi_*` metrics. Running
both just polls the GPUs twice. The Operator also installs the NVIDIA Container
Toolkit, an `nvidia` RuntimeClass, and the GPU Feature Discovery labels shown
above.

To replace `dcgm-exporter` instead of running both, set
`dcgmExporter.enabled=false` in the GPU Operator chart.

## Dynamic Resource Allocation (DRA)

On clusters running Kubernetes 1.34 or newer that manage GPUs with the
[NVIDIA DRA driver](https://github.com/kubernetes-sigs/dra-driver-nvidia-gpu)
(`dra-driver-nvidia-gpu`) and do not configure an `nvidia` `RuntimeClass`,
leave `runtimeClassName` empty. The DRA kubelet plugin injects `nvidia-smi`,
the driver libraries and the GPU device nodes into the container through CDI.

Label the exporter's namespace with `resource.kubernetes.io/admin-access: "true"`
and create a `ResourceClaimTemplate` in that namespace requesting all GPUs with
`adminAccess: true`. Because `adminAccess` is `true`, the claim gives the
exporter access to every GPU on the node without reserving them or blocking
workloads:

```yaml
apiVersion: resource.k8s.io/v1
kind: ResourceClaimTemplate
metadata:
  name: nvidia-gpu-exporter
spec:
  spec:
    devices:
      requests:
        - name: gpu
          exactly:
            deviceClassName: gpu.nvidia.com
            allocationMode: All
            adminAccess: true
```

Because `allocationMode: All` requires at least one matching GPU on the node
for the claim to allocate, restrict `nodeSelector` to GPU nodes on mixed
clusters so pods are not left `Pending` on CPU-only nodes:

```yaml
nodeSelector:
  nvidia.com/gpu.present: "true"
resourceClaims:
  - name: gpu
    resourceClaimTemplateName: nvidia-gpu-exporter
resources:
  claims:
    - name: gpu
```

## Restricted namespaces

A default install passes admission in namespaces enforcing the `restricted`
[Pod Security Standard](https://kubernetes.io/docs/concepts/security/pod-security-standards/).
The image runs as uid 65534, and the chart defaults declare what that level
requires:

```yaml
podSecurityContext:
  runAsNonRoot: true
  seccompProfile:
    type: RuntimeDefault
securityContext:
  allowPrivilegeEscalation: false
  capabilities:
    drop:
      - ALL
```

`hostNetwork`, `hostPort` and `hostPID` (which `computeApps` needs) are
rejected in `restricted` and `baseline` namespaces. A policy engine that
requires an explicit numeric uid rather than `runAsNonRoot` needs
`securityContext.runAsUser: 65534`, except on OpenShift, where leaving
`runAsUser` unset lets the `restricted-v2` SCC assign one.

## Upgrading

### Non-root images (1.15.0+)

Starting with app version 1.15.0, the container images run as uid 65534
instead of root, and the chart ships the security context defaults shown
above:

- A plain `helm upgrade` works unchanged for standard setups, and
  `--reuse-values` keeps your old security contexts.
- An `image.tag` pinned to a release older than 1.15.0 fails to start with
  `container has runAsNonRoot and image will run as root`. Unpin the tag or
  set `podSecurityContext.runAsNonRoot: null`.
- Setting `securityContext.privileged: true` on its own conflicts with the new
  defaults. Use the full block in [Per-process GPU metrics](#per-process-gpu-metrics).
- Overriding `securityContext` or `podSecurityContext` merges key by key, so
  `{}` changes nothing. Set a sub-key to `null` to drop one setting, or the
  whole key to `null` to drop the block.
- On hosts that restrict `/dev/nvidia*` permissions (`NVreg_DeviceFileMode` or
  a udev rule), relax the device mode, add the owning group via
  `podSecurityContext.supplementalGroups`, or run as root with
  `securityContext.runAsUser: 0` and `podSecurityContext.runAsNonRoot: null`.
- TLS material for `--web.config.file` mounted root-owned with
  `defaultMode: 0400` is unreadable to uid 65534, so use `0444` or an
  `fsGroup`.

### From chart 1.x

Chart 1.x lived in a [separate repository](https://github.com/utkuozdemir/helm-charts),
mounted host device files and driver libraries directly, and ran privileged.
Starting with chart 2.x, GPU access comes from the NVIDIA container runtime
and the pod runs unprivileged:

- Install the NVIDIA Container Toolkit on the nodes and set `runtimeClassName`.
- Drop any custom `volumes`, `volumeMounts`, or `securityContext` values from
  chart 1.x, as well as `ingress` values.
- The chart major is the app major plus one (chart `2.8.x` ships app `1.8.x`).

## Monitoring extras

All optional resources are off by default:

- `serviceMonitor` and `podMonitor` require the Prometheus Operator CRDs
  (enable one of them, not both).
- `prometheusRule` adds alerts on collection health and GPU health:
  driver-requested recovery actions, critical XID errors, uncorrectable ECC
  errors, memory row-remap and page-retirement state, hardware thermal
  slowdown and power braking. Rules that depend on fleet-specific thresholds
  (absolute temperature, GPU count) or that are normal on thermally-limited
  systems (software thermal slowdown) ship disabled, and each rule can be
  toggled individually. Some rules need the NVML backend (`xidCritical`,
  `xidWarning`, `pcieReplayRollovers`) or specific query fields (ECC, remapped
  rows), and stay inactive when the metric is absent. When upgrading a release
  that already had `prometheusRule.enabled: true`, newly added default-on
  rules arrive enabled, so review the list below and disable any you do not
  want.
- `grafanaDashboard` ships the [Grafana dashboard](https://grafana.com/grafana/dashboards/14574)
  and its [multi-GPU overview companion](https://grafana.com/grafana/dashboards/25547)
  as a ConfigMap labeled for the Grafana sidecar.

Restrict the DaemonSet to GPU nodes via `nodeSelector` or `affinity` before
enabling `prometheusRule`, otherwise the collection alerts fire on CPU-only
nodes. When installing multiple releases of this chart, enable `prometheusRule`
in only one of them.

With [kube-prometheus-stack](https://github.com/prometheus-community/helm-charts/tree/main/charts/kube-prometheus-stack),
match the stack's release selector label, relabel `instance` from the pod IP
to the node name so pod restarts do not split per-GPU series, and ensure the
Grafana sidecar watches all namespaces (`sidecar.dashboards.searchNamespace=ALL`):

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
| computeApps.enabled | bool | `false` | Also export per-process GPU metrics (`nvidia_smi_compute_app_*`). To see processes of other pods and containers, the exporter must share the host PID namespace: enable `hostPID` along with this. Note that the pid label changes as processes start and stop, creating short-lived series. |
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
| image.tag | string | `""` | Image tag (if not specified, defaults to the chart's appVersion). Set to a `-nvml` variant (for example `X.Y.Z-nvml`) for the recommended native NVML backend. |
| imagePullSecrets | list | `[]` | Image pull secrets, used by both the exporter and the `helm test` pods |
| livenessProbe | object | `{"httpGet":{"path":"/-/healthy","port":"http"}}` | Liveness probe for the exporter container. The default checks that the process serves HTTP at all. It deliberately does not depend on collection success, so a failing collection keeps the pod scrapeable and the failure visible in the metrics. Set to `null` to disable the probe. |
| log.format | string | `"logfmt"` | Log format: logfmt, json |
| log.level | string | `"info"` | Log level: debug, info, warn, error |
| nameOverride | string | `""` | Override the chart name |
| nodeSelector | object | `{"kubernetes.io/os":"linux"}` | Node selector for the pods. The images are Linux-only, hence the default. Add a GPU node label to keep the DaemonSet off non-GPU nodes, e.g. `nvidia.com/gpu.present: "true"` on clusters with GPU Feature Discovery (see the README section on scheduling). Helm merges maps key by key, so overriding with `{}` does not clear the default. Set the whole value to `null` to remove it. |
| nvidiaDriverCapabilities | string | `"utility"` | NVIDIA driver capability tier to request. `utility` is the nvidia-smi/NVML tier, which is all the exporter needs. |
| nvidiaSmiCommand | string | `"nvidia-smi"` | The command to run to get `nvidia-smi` compatible output. Can be a custom path and/or args. Note the official image contains only the exporter binary (no shell or ssh), so a wrapper command needs a custom image. |
| nvidiaVisibleDevices | string | `"all"` | Which GPUs to make visible to the exporter. `all` monitors every GPU on the node. |
| podAnnotations | object | `{}` | Annotations to add to the pods |
| podLabels | object | `{}` | Extra labels to add to the pods |
| podMonitor.additionalLabels | object | `{}` | Additional labels for the PodMonitor |
| podMonitor.enabled | bool | `false` | Create a Prometheus Operator PodMonitor instead of a ServiceMonitor (requires the Prometheus Operator CRDs). Enable either this or the ServiceMonitor, not both, otherwise the targets are scraped twice. |
| podMonitor.interval | string | `"15s"` | Scrape interval |
| podMonitor.metricRelabelings | list | `[]` | Relabelings to apply to the scraped metrics |
| podMonitor.relabelings | list | `[]` | Relabelings to apply to the scraped targets |
| podMonitor.scrapeTimeout | string | `""` | Scrape timeout |
| podSecurityContext | object | `{"runAsNonRoot":true,"seccompProfile":{"type":"RuntimeDefault"}}` | Security context for the pods. The defaults pass `restricted` Pod Security Standard admission. `runAsUser` is unset on purpose: the image's own uid applies, and an OpenShift SCC can still assign one. |
| port | int | `9835` | Port to listen on |
| priorityClassName | string | `""` | Priority class name for the pods |
| prometheusRule.additionalLabels | object | `{}` | Additional labels for the PrometheusRule, e.g. to match your Prometheus instance's rule selector |
| prometheusRule.additionalRuleLabels | object | `{}` | Labels added to every alert rule, e.g. to route the alerts in Alertmanager. A `severity` key here is ignored, since each rule sets its own severity. |
| prometheusRule.collectionFailing.enabled | bool | `true` | Alert when the most recent collection failed for some time |
| prometheusRule.collectionFailing.for | string | `"10m"` | How long collection must be failing before the alert fires |
| prometheusRule.collectionFailing.severity | string | `"warning"` | Severity label of the alert |
| prometheusRule.collectionSlow.enabled | bool | `true` | Alert when collecting GPU metrics is slow for 10 minutes. Healthy collections finish well under a second with the persistence daemon running, and sustained slowness often precedes a hung driver. |
| prometheusRule.collectionSlow.thresholdSeconds | int | `5` | Collection duration in seconds above which the alert fires |
| prometheusRule.collectionStale.enabled | bool | `true` | Alert when the last successful collection is too far in the past |
| prometheusRule.collectionStale.severity | string | `"warning"` | Severity label of the alert |
| prometheusRule.collectionStale.thresholdSeconds | int | `300` | Seconds since the last successful collection before the alert fires |
| prometheusRule.computeAppsInvisible.enabled | bool | `true` | Alert when a GPU has more than 1 GiB of memory in use for 30 minutes while `nvidia_smi_compute_apps` reports zero processes, which catches `computeApps.enabled: true` without `hostPID: true`. Silently inactive when `computeApps.enabled` is false. |
| prometheusRule.enabled | bool | `false` | Create a Prometheus Operator PrometheusRule with default alerts (requires the Prometheus Operator CRDs). In clusters where the exporter also runs on nodes without GPUs, restrict the DaemonSet to GPU nodes via nodeSelector or affinity before enabling this, otherwise the alerts fire for nodes that cannot collect GPU metrics by design. |
| prometheusRule.gpuMissing.enabled | bool | `false` | Alert when a machine reports fewer GPUs than its recent maximum while collection stays healthy, which catches GPUs that silently fall off the bus on the default backend. Off by default: it also fires for up to 6 hours after deliberately removing a GPU and needs a stable instance label across exporter restarts. |
| prometheusRule.idleVramAllocated.enabled | bool | `false` | Informational alert when a GPU holds more than 50% of its VRAM allocated while its peak compute utilization over the last hour stays below 5%. Off by default: idle inference servers keeping weights loaded in VRAM are often intentional. |
| prometheusRule.pcieLinkGenDegraded.enabled | bool | `true` | Alert when a GPU runs at a lower PCIe link generation than its maximum while above 50% compute utilization for 10 minutes. Idle GPUs downshift PCIe generation to save power, which is normal. |
| prometheusRule.pcieLinkWidthDegraded.enabled | bool | `false` | Alert when a GPU negotiates fewer PCIe lanes than its maximum width while above 50% compute utilization for 10 minutes. Off by default: cards intentionally installed in narrower PCIe slots report a lower current width permanently. |
| prometheusRule.pcieReplayRollovers.enabled | bool | `false` | Alert when a GPU records PCIe link replay rollovers in the last hour. Off by default: requires the NVML backend. |
| prometheusRule.powerBrake.enabled | bool | `true` | Alert when an external hardware power brake throttles a GPU for 5 minutes: a power delivery problem (PSU, cables, rack power). The configured software power limit never raises this. |
| prometheusRule.recoveryAction.enabled | bool | `true` | Alert when the driver itself requests a GPU recovery action (reset, reboot, drain). The most direct health signal there is. Needs a recent driver (570+), silently absent on older ones. |
| prometheusRule.retiredPagesPending.enabled | bool | `true` | Alert when a GPU memory page retirement waits for a reboot to take effect (pre-Ampere GPUs) |
| prometheusRule.rowRemapFailure.enabled | bool | `true` | Alert when GPU memory row remapping failed, which is NVIDIA's stated RMA criterion |
| prometheusRule.rowRemapPending.enabled | bool | `true` | Alert when a GPU memory row remap waits for a GPU reset to take effect |
| prometheusRule.swThermalSlowdown.enabled | bool | `false` | Alert when the driver throttles a GPU at its temperature target for 30 minutes. Off by default: this is normal on thermally-limited consumer and laptop systems under sustained load. |
| prometheusRule.temperatureHigh.enabled | bool | `false` | Alert when the GPU core temperature stays above the threshold for 10 minutes. Off by default: safe temperatures differ per GPU generation (a datacenter A100 throttles around 83C while laptop GPUs sit at 87C by design), so tune the threshold to your fleet before enabling. |
| prometheusRule.temperatureHigh.thresholdCelsius | int | `85` | Core temperature in Celsius above which the alert fires |
| prometheusRule.temperatureTlimitLow.enabled | bool | `true` | Alert when a GPU has less than the threshold of thermal headroom remaining before slowdown (`temperature.gpu.tlimit`) for 10 minutes. Uses the GPU's own per-SKU target temperature, and is silently absent on older GPUs and drivers that do not report `temperature.gpu.tlimit`. |
| prometheusRule.temperatureTlimitLow.thresholdCelsius | int | `5` | Remaining thermal headroom in Celsius below which the alert fires |
| prometheusRule.thermalSlowdown.enabled | bool | `true` | Alert when hardware thermal protection throttles a GPU for 5 minutes: cooling is failing at the hardware trip point. The driver applies the right per-model threshold, so this works across GPU generations. Disable if it fires routinely on thermally-limited mobile/small-form-factor systems. |
| prometheusRule.uncorrectableEcc.enabled | bool | `true` | Alert when a GPU reports uncorrectable ECC memory errors since its last reset. Data may have been corrupted, and a GPU reset repairs the memory on Ampere and newer. Corrected (single-bit) errors never alert. |
| prometheusRule.xidCritical.codes | list | `[48,62,64,74,79,95,119,120]` | Xid codes treated as critical (reset/reboot class) |
| prometheusRule.xidCritical.enabled | bool | `true` | Alert on Xid errors whose NVIDIA-recommended action is a GPU reset or node reboot. Needs the NVML backend and never fires on the `exec` backend. |
| prometheusRule.xidWarning.codes | list | `[94]` | Xid codes treated as warning (restart-app class) |
| prometheusRule.xidWarning.enabled | bool | `true` | Alert on Xid errors whose recommended action is an application restart. Explicit allowlist: application-caused Xids (13, 31, 43, ...) and informational ones (63, 92) deliberately never alert. |
| queryFieldNames | list | `["AUTO"]` | `nvidia-smi` fields to be queried by the exporter. `AUTO` auto-detects them. |
| queryFieldNamesExclude | list | `[]` | `nvidia-smi` fields to exclude from being queried. Names match literally, with `*` as a wildcard for any sequence of characters. |
| readinessProbe | object | `{"httpGet":{"path":"/-/ready","port":"http"}}` | Readiness probe for the exporter container, process-level like the liveness probe. Set to `null` to disable the probe. |
| resourceClaims | list | `[]` | Resource claims for the pods, referenced from `resources.claims` |
| resources | object | `{}` | Resources for the exporter container |
| revisionHistoryLimit | string | `""` | How many old DaemonSet history revisions to retain for rollbacks. Empty means the Kubernetes default (10). |
| runtimeClassName | string | `""` | Name of the RuntimeClass to run the pods with. GPU access is injected by the NVIDIA container runtime or by Dynamic Resource Allocation (DRA, via `resourceClaims`). Set this to the name of your NVIDIA RuntimeClass (usually `nvidia`), or leave it empty when the NVIDIA runtime is the default runtime of your nodes or when using DRA. If none of those holds, the exporter will come up but serve no GPU metrics, reporting `nvidia_smi_last_collect_success 0`. |
| securityContext | object | `{"allowPrivilegeEscalation":false,"capabilities":{"drop":["ALL"]},"readOnlyRootFilesystem":true}` | Security context for the exporter container. Unprivileged: GPU access comes from the NVIDIA runtime, which needs no privileges or capabilities. The exporter writes no files, so the root filesystem is read-only. A custom image with a wrapper command that writes files (e.g., ssh's known_hosts) needs `readOnlyRootFilesystem: false`. |
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
