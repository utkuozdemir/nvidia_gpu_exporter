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
#
# With --screenshot, a second copy of each dashboard is written for
# screenshots.sh to render: same dashboard with every collapsed row expanded,
# because the image renderer photographs a collapsed row as a collapsed row.
# Those copies are only written when asked, so the dev stack's dashboard list
# stays uncluttered, and the next plain run removes them again.
set -euo pipefail

here="$(cd "$(dirname "$0")" && pwd)"

screenshot_variants=false
case "${1:-}" in
  --screenshot) screenshot_variants=true ;;
  "") ;;
  # a typo would otherwise provision the ordinary dashboards and leave the
  # capture to fail later against a dashboard that was never written
  *)
    echo "usage: $(basename "$0") [--screenshot]" >&2
    exit 2
    ;;
esac

# the output directory is render output, nothing else: clear it first so a
# stale file from an older revision of the stack cannot linger as a ghost
# dashboard in Grafana
rm -f "$here/grafana/dashboards/"*.json

render() {
  local src="$here/../../docs/grafana/$1"
  local dst="$here/grafana/dashboards/$2"
  local mode="$3"

  mkdir -p "$(dirname "$dst")"
  python3 - "$src" "$dst" "$mode" << 'PY'
import json
import sys

src, dst, mode = sys.argv[1], sys.argv[2], sys.argv[3]

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


def expand_rows(dashboard):
    """Open every collapsed row, the way a reader would before screenshotting.

    A collapsed row keeps its children nested under itself and out of the
    dashboard's own panel list, so flipping the flag alone yields an open row
    with nothing in it. The children have to be lifted back out, and every
    panel below them pushed down by however much room they take.

    Positions are rewritten relative to the top of each group rather than
    assigned one panel at a time, which is what keeps panels that sit side by
    side sitting side by side.
    """
    expected = sum(1 + len(p.get("panels", [])) for p in dashboard["panels"])
    panels, cursor = [], 0

    def place(group, top):
        if not group:
            return top
        first = min(panel["gridPos"]["y"] for panel in group)
        bottom = top
        for panel in group:
            panel["gridPos"]["y"] += top - first
            bottom = max(bottom, panel["gridPos"]["y"] + panel["gridPos"]["h"])
        panels.extend(group)
        return bottom

    def occupied(panel):
        box = panel["gridPos"]
        return {
            (x, y)
            for x in range(box["x"], box["x"] + box["w"])
            for y in range(box["y"], box["y"] + box["h"])
        }

    group = []
    for panel in dashboard["panels"]:
        if panel["type"] != "row":
            group.append(panel)
            continue
        cursor = place(group, cursor)
        group = []
        children = panel.pop("panels", [])
        panel["collapsed"] = False
        panel["gridPos"]["y"] = cursor
        panels.append(panel)
        cursor = place(children, cursor + 1)
    place(group, cursor)

    # A silently mangled layout still photographs perfectly well, so the
    # transform checks its own work rather than leaving the picture to say it.
    if len(panels) != expected:
        raise SystemExit(f"expanding rows lost panels: {len(panels)} of {expected}")

    seen = set()
    for panel in panels:
        cells = occupied(panel)
        if cells & seen:
            raise SystemExit(f"expanding rows overlapped panel {panel.get('title')!r}")
        seen |= cells

    dashboard["panels"] = panels


if mode == "screenshot":
    expand_rows(dashboard)
    dashboard["uid"] = "shot-" + dashboard["uid"]
    dashboard["title"] += " (screenshot)"
    # the published dashboards find each other by tag, and a tagged copy would
    # show up in the real ones' related-dashboards row for every ordinary user
    # of the dev stack. Its own row goes too: a real dashboard lists only its
    # counterpart there, while an untagged copy would list both and put a
    # second button in the picture that nobody else gets.
    dashboard["tags"] = []
    dashboard["links"] = []

with open(dst, "w") as f:
    json.dump(dashboard, f, indent=2)
    f.write("\n")
PY
  echo "rendered $dst"
}

render dashboard.json nvidia-gpu-metrics.json dev
render dashboard-overview.json nvidia-gpu-overview.json dev

if [[ $screenshot_variants == true ]]; then
  render dashboard.json shot-nvidia-gpu-metrics.json screenshot
  render dashboard-overview.json shot-nvidia-gpu-overview.json screenshot
fi
