#!/usr/bin/env bash
# Render the Helm chart's PrometheusRule into a plain Prometheus rule file for
# the dev stack, so the stack always tests exactly what the chart ships - no
# hand-maintained copy. Every rule is force-enabled (the chart keeps a few off
# by default), the `for:` durations are shortened so the local
# fire-every-alert loop takes seconds, not tens of minutes, and the slow
# threshold drops to 2s because the stack's 5s scrape interval caps the scrape
# timeout below the chart's 5s default (a slower fake would just fail the
# scrape instead of proving the slow alert).
#
# Helm and yq run through docker on pinned images: the dev stack's host
# dependencies stay docker + python3.
set -euo pipefail

HELM_IMAGE=alpine/helm:3.19.0
YQ_IMAGE=mikefarah/yq:4.48.1
DEV_FOR=30s

here="$(cd "$(dirname "$0")" && pwd)"
repo="$(cd "$here/../.." && pwd)"
out_dir="$here/prometheus/rules"

mkdir -p "$out_dir"
rm -f "$out_dir"/*.yml

# derive the default-off rules from the chart's values.yaml instead of
# hardcoding them, so a new rule cannot silently escape the dev stack
enable_flags=()
while IFS= read -r rule; do
  enable_flags+=("--set" "prometheusRule.$rule.enabled=true")
done < <(docker run --rm -i "$YQ_IMAGE" eval \
  '.prometheusRule | to_entries[] | select((.value | type) == "!!map" and (.value | has("enabled")) and (.value.enabled == false)) | .key' \
  - < "$repo/charts/nvidia-gpu-exporter/values.yaml")

docker run --rm -v "$repo/charts/nvidia-gpu-exporter:/chart:ro" "$HELM_IMAGE" \
  template dev /chart \
  --show-only templates/prometheusrule.yaml \
  --set prometheusRule.enabled=true \
  "${enable_flags[@]}" \
  --set prometheusRule.collectionSlow.thresholdSeconds=2 \
  --set prometheusRule.collectionStale.thresholdSeconds=60 |
  docker run --rm -i "$YQ_IMAGE" eval '
    {"groups": .spec.groups}
    | (.groups[].rules[] | select(has("for")) | .for) = "'"$DEV_FOR"'"
  ' - > "$out_dir/nvidia-gpu-exporter.yml"

echo "rendered $out_dir/nvidia-gpu-exporter.yml"
