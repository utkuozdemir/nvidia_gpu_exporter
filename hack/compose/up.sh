#!/usr/bin/env bash
# Bring up the dev stack: render the dashboard, then start compose. Extra args
# are passed through to `docker compose up` (e.g. -d, --build).
set -euo pipefail

here="$(cd "$(dirname "$0")" && pwd)"
"$here/render-dashboard.sh"
cd "$here"
exec docker compose up --build "$@"
