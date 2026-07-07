#!/usr/bin/env bash
# Copy the published dashboard into a provisioning copy for the dev stack.
#
# docs/grafana/dashboard.json is the published artifact (grafana.com 14574).
# It selects its data source through a template variable, which resolves to
# this stack's sole Prometheus on its own, so no substitution is needed. The
# published artifact ships non-editable, so the dev copy flips that one flag
# back, otherwise the author-in-the-UI loop below would be blocked
# (allowUiUpdates in the provider does not override the dashboard model).
# Grafana's provider polls the output, so re-run this after editing the
# dashboard to see the change live.
set -euo pipefail

here="$(cd "$(dirname "$0")" && pwd)"
src="$here/../../docs/grafana/dashboard.json"
dst="$here/grafana/dashboards/nvidia-gpu-metrics.json"

mkdir -p "$(dirname "$dst")"
sed 's/"editable": false/"editable": true/' "$src" > "$dst"
echo "rendered $dst"
