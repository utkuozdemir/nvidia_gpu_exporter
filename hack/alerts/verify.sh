#!/usr/bin/env bash
# Validate the Helm chart's alert rules without any cluster:
#   1. render the PrometheusRule with every rule enabled, at real durations
#   2. promtool check rules
#   3. promtool unit tests (tests.yaml) against an annotation-stripped copy,
#      so the behavior tests don't break on wording changes
#   4. static assertions: the Xid critical/warning code lists stay disjoint,
#      and the annotations carry the corrective actions
# Needs only docker + python3; helm, yq and promtool run from pinned images.
set -euo pipefail

HELM_IMAGE=alpine/helm:3.19.0
YQ_IMAGE=mikefarah/yq:4.48.1
PROM_IMAGE=prom/prometheus:v3.13.1

here="$(cd "$(dirname "$0")" && pwd)"
repo="$(cd "$here/../.." && pwd)"
out="$here/rendered"

mkdir -p "$out"
rm -f "$out"/*.yml "$out"/*.yaml

chart="$repo/charts/nvidia-gpu-exporter"

# derive the rule list from values.yaml instead of hardcoding it, so a new
# rule cannot silently escape CI coverage
rule_keys() { # $1: true|false|any - filter by the enabled default
  docker run --rm -i "$YQ_IMAGE" eval \
    '.prometheusRule | to_entries[] | select((.value | type) == "!!map" and (.value | has("enabled"))'"$([ "$1" = any ] || echo " and (.value.enabled == $1)")"') | .key' \
    - < "$chart/values.yaml"
}

enable_flags=()
while IFS= read -r rule; do
  enable_flags+=("--set" "prometheusRule.$rule.enabled=true")
done < <(rule_keys false)

docker run --rm -v "$chart:/chart:ro" "$HELM_IMAGE" \
  template ci /chart \
  --show-only templates/prometheusrule.yaml \
  --set prometheusRule.enabled=true \
  "${enable_flags[@]}" |
  docker run --rm -i "$YQ_IMAGE" eval '{"groups": .spec.groups}' - > "$out/rules.yml"

total="$(rule_keys any | wc -l | tr -d ' ')"
rendered="$(grep -c "alert:" "$out/rules.yml")"
if [ "$rendered" != "$total" ]; then
  echo "FAIL: full render produced $rendered rules, expected $total (a rule escaped the render?)" >&2
  exit 1
fi

docker run --rm -i "$YQ_IMAGE" eval 'del(.groups[].rules[].annotations)' - \
  < "$out/rules.yml" > "$out/rules-noann.yml"

echo "--- render edge cases"
# 1. --reuse-values upgrade simulation: an old release's values object has
# NONE of the new rule subtrees (helm does not backfill new chart defaults
# on --reuse-values, see helm/helm#8085). Every subtree nulled must render
# exactly the rules whose values.yaml default is enabled, proving the
# template's dig fallbacks stay in lockstep with values.yaml.
{
  echo "prometheusRule:"
  echo "  enabled: true"
  while IFS= read -r rule; do echo "  $rule: null"; done < <(rule_keys any)
} > "$out/upgrade-sim-values.yaml"
expected="$(rule_keys true | wc -l | tr -d ' ')"
got="$(docker run --rm -v "$chart:/chart:ro" -v "$out:/w:ro" "$HELM_IMAGE" \
  template ci /chart -f /w/upgrade-sim-values.yaml \
  --show-only templates/prometheusrule.yaml | grep -c "alert:")"
if [ "$got" != "$expected" ]; then
  echo "FAIL: upgrade simulation rendered $got rules, expected $expected (dig defaults drifted from values.yaml?)" >&2
  exit 1
fi
echo "upgrade simulation renders $got default-on rules"

# 2. enabled=true with every rule disabled must suppress the resource
disable_flags=()
while IFS= read -r rule; do
  disable_flags+=("--set" "prometheusRule.$rule.enabled=false")
done < <(rule_keys any)
kinds="$(docker run --rm -v "$chart:/chart:ro" "$HELM_IMAGE" \
  template ci /chart --set prometheusRule.enabled=true "${disable_flags[@]}" |
  grep -c "kind: PrometheusRule" || true)"
if [ "$kinds" != "0" ]; then
  echo "FAIL: all-rules-disabled still rendered $kinds PrometheusRule resource(s)" >&2
  exit 1
fi
echo "all-rules-disabled suppresses the resource"

echo "--- promtool check rules"
docker run --rm -v "$here:/w" --entrypoint /bin/promtool "$PROM_IMAGE" \
  check rules /w/rendered/rules.yml

echo "--- promtool test rules"
docker run --rm -v "$here:/w" --entrypoint /bin/promtool "$PROM_IMAGE" \
  test rules /w/tests.yaml

echo "--- static assertions"
python3 - "$out/rules.yml" << 'PY'
import re
import sys

text = open(sys.argv[1]).read()

def fail(msg):
    sys.exit(f"FAIL: {msg}")

# the two Xid allowlists must stay disjoint, or one event pages twice with
# conflicting guidance
regexes = re.findall(r'xid=~"([0-9|]+)"', text)
if len(regexes) != 2:
    fail(f"expected exactly 2 xid regexes, found {len(regexes)}")
critical, warning = ({s for s in r.split("|")} for r in regexes)
if critical & warning:
    fail(f"xid code lists overlap: {sorted(critical & warning)}")

# every alert must tell the operator what to do; spot-check the load-bearing
# corrective actions survive template/wording edits
required = {
    "NvidiaGpuRecoveryActionNeeded": ["1 = reset the GPU", "2 = reboot the node", "current value:"],
    "NvidiaGpuXidCritical": ["Drain the GPU", "reboot the node", "dmesg"],
    "NvidiaGpuXidWarning": ["restart the", "application"],
    "NvidiaGpuUncorrectableEccErrors": ["reset", "RMA"],
    "NvidiaGpuRowRemapFailure": ["RMA"],
    "NvidiaGpuRowRemapPending": ["reset"],
    "NvidiaGpuRetiredPagesPending": ["reboot"],
    "NvidiaGpuThermalSlowdown": ["fans", "airflow"],
    "NvidiaGpuSoftwareThermalSlowdown": ["cooling"],
    "NvidiaGpuPowerBrake": ["PSU", "cables"],
    "NvidiaGpuExporterCollectionSlow": ["persistence"],
    "NvidiaGpuMissing": ["Xid 79", "reseat"],
}
blocks = re.split(r"- alert: ", text)[1:]
byname = {b.split("\n", 1)[0].strip(): b for b in blocks}
for alert, needles in required.items():
    block = byname.get(alert) or fail(f"alert {alert} not rendered")
    for needle in needles:
        if needle not in block:
            fail(f"{alert}: annotation lost its corrective action ({needle!r})")

print("static assertions OK")
PY

echo "alert rules OK"
