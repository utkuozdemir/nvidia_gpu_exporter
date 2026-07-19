#!/usr/bin/env bash
# Bring up the dev stack: render the dashboards, validate the compose file,
# then start compose. Extra args are passed through to `docker compose up`
# (e.g. -d, --build).
set -euo pipefail

here="$(cd "$(dirname "$0")" && pwd)"
"$here/render-dashboard.sh"
"$here/render-rules.sh"
cd "$here"
docker compose config --quiet
exec docker compose up --build "$@"
