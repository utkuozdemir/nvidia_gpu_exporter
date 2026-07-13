#!/usr/bin/env bash
# Copy the published dashboards into provisioning copies for the dev stack.
#
# docs/grafana/dashboard.json (grafana.com 14574) and
# docs/grafana/dashboard-overview.json are the published artifacts. They
# select their data source through a template variable, which resolves to
# this stack's sole Prometheus on its own, so no substitution is needed. The
# published artifacts ship non-editable, so the dev copies flip that one flag
# back, otherwise the author-in-the-UI loop below would be blocked
# (allowUiUpdates in the provider does not override the dashboard model).
# Grafana's provider polls the output, so re-run this after editing a
# dashboard to see the change live.
set -euo pipefail

here="$(cd "$(dirname "$0")" && pwd)"

render() {
  local src="$here/../../docs/grafana/$1"
  local dst="$here/grafana/dashboards/$2"

  mkdir -p "$(dirname "$dst")"
  sed 's/"editable": false/"editable": true/' "$src" > "$dst"
  echo "rendered $dst"
}

render dashboard.json nvidia-gpu-metrics.json
render dashboard-overview.json nvidia-gpu-overview.json
