#!/usr/bin/env bash
# Copy the published dashboards into provisioning copies for the dev stack.
#
# docs/grafana/dashboard.json (grafana.com 14574) and
# docs/grafana/dashboard-overview.json are the published artifacts. The dev
# copies differ in exactly two ways:
#   - editable is flipped back on (the published artifacts ship
#     non-editable, which would block the author-in-the-UI loop;
#     allowUiUpdates in the provider does not override the dashboard model)
#   - the data source variable is preselected to the demo Prometheus, the
#     richer surface (MIG/XID/energy/PCIe), where dashboard work happens;
#     the dropdown still flips to the exec flavor at any time
# Grafana's provider polls the output, so re-run this after editing a
# dashboard to see the change live.
set -euo pipefail

here="$(cd "$(dirname "$0")" && pwd)"

# the output directory is render output, nothing else: clear it first so a
# stale file from an older revision of the stack cannot linger as a ghost
# dashboard in Grafana
rm -f "$here/grafana/dashboards/"*.json

render() {
  local src="$here/../../docs/grafana/$1"
  local dst="$here/grafana/dashboards/$2"

  mkdir -p "$(dirname "$dst")"
  python3 - "$src" "$dst" << 'PY'
import json
import sys

src, dst = sys.argv[1], sys.argv[2]

with open(src) as f:
    dashboard = json.load(f)

dashboard["editable"] = True

for variable in dashboard.get("templating", {}).get("list", []):
    if variable.get("type") == "datasource":
        variable["current"] = {
            "selected": True,
            "text": "Prometheus - NVML",
            "value": "prometheus-demo",
        }

with open(dst, "w") as f:
    json.dump(dashboard, f, indent=2)
    f.write("\n")
PY
  echo "rendered $dst"
}

render dashboard.json nvidia-gpu-metrics.json
render dashboard-overview.json nvidia-gpu-overview.json
