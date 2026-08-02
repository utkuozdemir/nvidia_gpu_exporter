#!/usr/bin/env bash
# Regenerate the dashboard images the README shows, headlessly.
#
# The pictures come from the dev stack's NVML (demo backend) flavor, the
# richer surface, photographed by Grafana's own image renderer. There is no
# browser script: the renderer is a sidecar Grafana talks to, and a capture is
# an HTTP GET. The one thing it will not do is open a collapsed row, which is
# why render-dashboard.sh --screenshot writes row-expanded copies of the two
# dashboards for this script to point at.
#
# Every run produces different pixels, because the demo data jitters and the
# clock moves. That is why nothing runs this on a schedule: the images are
# refreshed when a dashboard changes, and the diff is reviewed by eye.
#
# Host requirements are the stack's own: docker, python3 and curl. The palette
# pass runs from a pinned image, the way helm and yq do in render-rules.sh.
set -euo pipefail

here="$(cd "$(dirname "$0")" && pwd)"

# where the two images land. Point it somewhere else to look at a run without
# touching the committed ones.
out="${OUT_DIR:-$here/../../docs/grafana}"
mkdir -p "$out"

# How much history the pictures show, and therefore how long a cold stack has
# to run before it can be photographed: the graphs are only as wide as the data
# behind them. Deliberately short. A warm stack pays nothing either way, so
# raise it when there is time to spare and the lines will be denser.
window="${WINDOW:-2m}"

# wide enough that the overview's GPU table shows its last column rather than
# clipping it
width="${WIDTH:-1920}"

# Rendered at twice that, the way a screen capture from a high density display
# would be, because README images are displayed at around 880 pixels and the
# spare detail is what keeps small axis labels readable after the browser
# downsamples them.
scale="${SCALE:-2}"

# Which simulated machine gets photographed. Not configurable, because the
# committed images have one deliberate subject and every check below is written
# for it. The datacenter box is the only one that populates the MIG and XID
# panels, which are half the reason the pictures come from the NVML surface at
# all. The consumer box was tried: eight cards make a livelier table and
# throttle timeline, but leave that whole row empty.
machine=datacenter

grafana="${GRAFANA_URL:-http://localhost:3000}"
prometheus="${PROMETHEUS_URL:-http://localhost:9091}"
ffmpeg_image="${FFMPEG_IMAGE:-mwader/static-ffmpeg:8.0}"

# derived, so renaming a dashboard's uid does not turn into a wait_for timeout
detail_uid="shot-$(python3 -c 'import json, sys; print(json.load(open(sys.argv[1]))["uid"])' "$here/../../docs/grafana/dashboard.json")"
overview_uid="shot-$(python3 -c 'import json, sys; print(json.load(open(sys.argv[1]))["uid"])' "$here/../../docs/grafana/dashboard-overview.json")"

work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

log() { echo "==> $*"; }

promquery() {
  local query="$1" at="${2:-}"
  curl -sf --get "$prometheus/api/v1/query" \
    --data-urlencode "query=$query" ${at:+--data-urlencode "time=$at"}
}

seconds() {
  python3 -c 'import sys; u = sys.argv[1]; print(int(u[:-1]) * {"s": 1, "m": 60, "h": 3600}[u[-1]])' "$1"
}

wait_for() {
  local what="$1" deadline=$((SECONDS + ${2}))
  shift 2
  until "$@" > /dev/null 2>&1; do
    if ((SECONDS > deadline)); then
      echo "timed out waiting for $what" >&2
      return 1
    fi
    sleep 2
  done
}

has_series() {
  promquery "$1" "${2:-}" | python3 -c 'import json, sys; sys.exit(0 if json.load(sys.stdin)["data"]["result"] else 1)'
}

# Prometheus is asked two separate things before anything is photographed: is
# it still being fed, and does it hold a sample from the far edge of the window
# about to be drawn. Both are needed. The volumes outlive a `docker compose
# down`, so a stack with an hour of history and a wedged exporter would satisfy
# coverage alone and be photographed dead.
data_is_fresh() {
  has_series "count(time() - timestamp(nvidia_smi_index{instance=\"$machine\"}) < 20) > 0"
}

covers() {
  # Asked at the far edge of the window, and asked for a sample that is recent
  # *there*: a plain selector would be answered out of Prometheus's five-minute
  # lookback, so an exporter that died three minutes ago and has just come back
  # would satisfy the left edge with a sample that is not in the window at all.
  has_series "timestamp(nvidia_smi_index{instance=\"$machine\"}) > time() - 20" \
    "$(($(date +%s) - $(seconds "$1")))"
}

has_nvml_families() {
  # each asked for freshly, not merely present: Prometheus would answer a bare
  # selector out of its five-minute lookback, so a family that stopped being
  # served during this run would still satisfy it
  local family
  for family in nvidia_smi_mig_info nvidia_smi_energy_joules_total nvidia_smi_xid_errors_total; do
    has_series "count(time() - timestamp($family{instance=\"$machine\"}) < 20) > 0" || return 1
  done
}

uuid_of() {
  local instance="$1" index="$2"
  promquery "nvidia_smi_gpu_info{instance=\"$instance\",index=\"$index\"}" |
    python3 -c 'import json, sys
result = json.load(sys.stdin)["data"]["result"]
if not result:
    raise SystemExit("no gpu_info series, is the demo flavor up?")
print(result[0]["metric"]["uuid"])'
}

# The renderer answers with a PNG or with an error page, and both arrive as
# 200s often enough to be worth checking. What comes back is checked against
# what was asked for rather than against the exit code, because the interesting
# failures all report success: an error page renders at exactly the size
# requested, a collapsed dashboard photographs perfectly well, and a renderer
# that ignores a parameter returns a valid PNG of the wrong thing.
render() {
  local dst="$1" min_height="$2" url="$3"

  local attempt status
  for attempt in 1 2 3; do
    status="$(curl -s --max-time 300 -o "$dst" -w "%{http_code}" "$url")" || status="000"
    if [[ $status == 200 ]] && check_png "$dst" "$min_height"; then
      return 0
    fi
    # said out loud, because a bare exit status in a workflow log explains
    # nothing about which of these went wrong
    echo "render attempt $attempt failed (http $status): $url" >&2
    if [[ $status != 200 && -s $dst ]]; then
      head -c 300 "$dst" >&2
      echo >&2
    fi
    if ((attempt < 3)); then sleep 5; fi
  done
  echo "could not render $url" >&2
  return 1
}

check_png() {
  python3 -c '
import struct
import sys

path, min_height, want_width = sys.argv[1], int(sys.argv[2]), int(sys.argv[3])
with open(path, "rb") as f:
    header = f.read(24)
if header[:8] != b"\x89PNG\r\n\x1a\n":
    raise SystemExit(f"{path} is not a PNG")
width, height = struct.unpack(">II", header[16:24])
# the width is the requested one multiplied by the pixel ratio, so this is also
# what catches a renderer that quietly ignored either parameter
if width != want_width:
    raise SystemExit(f"{path} is {width}px wide, asked for {want_width}")
if height < min_height:
    raise SystemExit(f"{path} is {width}x{height}, expected at least {min_height} tall")
' "$1" "$2" "$((width * scale))"
}

url_for() {
  local uid="$1" from="$2" to="$3" height="$4" scale="$5"
  shift 5
  local url="$grafana/render/d/$uid/x?orgId=1&kiosk&theme=dark&timeout=120"
  url+="&width=$width&height=$height&scale=$scale&from=$from&to=$to"
  local var
  for var in "$@"; do
    url+="&$var"
  done
  echo "$url"
}

# Before compose, because compose creates a missing bind-mount source itself
# and on Linux the daemon does that as root, which leaves the render below
# unable to write into its own output directory. The directory is gitignored,
# so a fresh checkout is exactly where this bites.
mkdir -p "$here/grafana/dashboards"

log "starting the stack with the screenshot profile"
# Named services rather than the whole stack: the pictures are of one machine's
# NVML surface, so the exec flavors, their Prometheus and Alertmanager are work
# a cold runner should not do. --no-deps because Grafana's ordinary dependency
# chain reaches back into them. Prometheus goes on trying to scrape the other
# machine's exporter and to reach an Alertmanager that is not there, which is
# log noise and nothing more: every panel query is scoped to one instance.
#
# Unconditionally, even when the stack looks up: this converges the
# configuration of one someone left hand-edited, and starts the renderer, which
# an ordinary up.sh does not.
COMPOSE_PROFILES=screenshot docker compose \
  --project-directory "$here" -f "$here/docker-compose.yaml" \
  up -d --build --no-deps "nvml-$machine" prometheus-demo grafana renderer

log "waiting for grafana"
wait_for "grafana" 120 curl -sf "$grafana/api/health"

# only now, so that a Grafana that compose decided to recreate cannot come up
# racing the files being written underneath it. Its provider polls, so it picks
# them up within a few seconds of them appearing.
log "writing the expanded dashboards"
"$here/render-dashboard.sh" --screenshot
wait_for "the expanded detail dashboard" 120 curl -sf "$grafana/api/dashboards/uid/$detail_uid"
wait_for "the expanded overview dashboard" 120 curl -sf "$grafana/api/dashboards/uid/$overview_uid"

log "waiting for the demo backend to be scraped"
wait_for "fresh demo data" 180 data_is_fresh
# The families that are the whole reason these pictures come from the NVML
# surface. Without this a demo backend that quietly stopped serving them still
# yields tall, valid PNGs full of "No data", and the workflow opens a pull
# request proposing them.
wait_for "the NVML-only families" 60 has_nvml_families

# the one wait a cold stack pays: this is how far back the pictures reach
log "waiting for $window of history"
wait_for "$window of history" "$(($(seconds "$window") + 120))" covers "$window"

gpu="$(uuid_of "$machine" 0)"
log "photographing $machine gpu 0 ($gpu)"

# Full-page stills. height=-1 asks the renderer for the whole page rather than a
# viewport, which is the entire reason this needs no scrolling or stitching.
#
# The height floor is what catches a capture of the dashboards with their rows
# still collapsed, which is otherwise a perfectly good picture of the wrong
# thing. Unscaled, expanded pages run past 3000 pixels and the taller collapsed
# one comes to about 1900, so the floor sits between them and follows the scale.
render "$work/dashboard.png" $((2400 * scale)) \
  "$(url_for "$detail_uid" "now-$window" now -1 "$scale" "var-node=$machine" "var-gpu=$gpu")"
render "$work/dashboard-overview.png" $((2400 * scale)) \
  "$(url_for "$overview_uid" "now-$window" now -1 "$scale" "var-node=$machine")"

# Down to a palette, which is what makes rendering at twice the size affordable:
# a dashboard is mostly flat colour, so a hundred-odd of them carry the whole
# picture and the file ends up smaller than the unscaled truecolor one it
# replaces. No dithering: on flat panels it is noise that costs bytes.
log "reducing to a palette"
for image in dashboard dashboard-overview; do
  docker run --rm -u "$(id -u):$(id -g)" -v "$work:/work" -w /work "$ffmpeg_image" \
    -y -loglevel error -i "$image.png" \
    -vf "split[a][b];[a]palettegen=max_colors=128[p];[b][p]paletteuse=dither=none" \
    -compression_level 100 "$image-palette.png"
  # same check as after the render: the palette pass is another tool that can
  # report success and hand back something that is not the picture
  check_png "$work/$image-palette.png" $((2400 * scale))
  mv "$work/$image-palette.png" "$work/$image.png"
done

# Neither is replaced until both have been produced, so a run that dies halfway
# cannot leave the repository with one new image and one old one.
mv "$work/dashboard.png" "$work/dashboard-overview.png" "$out/"

log "wrote:"
ls -la "$out/dashboard.png" "$out/dashboard-overview.png"
