#!/usr/bin/env bash
# Browser gate runner for web-frontend 단언 O·P·Q·R (Playwright, in-container).
# All execution happens inside an ephemeral --rm Playwright container attached to
# the sentinel docker network. Nothing is installed on the host; @playwright/test
# is cached in a named docker volume so repeat runs skip the npm install.
#
# Usage: ./run_browser_gates.sh [playwright-test-args...]
#   NET=sentinel_sentinel-net BASE_URL=http://web-frontend:80 ./run_browser_gates.sh o_ws_state.spec.ts
set -uo pipefail
GATE_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
NET="${NET:-sentinel_sentinel-net}"
IMG="${PW_IMG:-mcr.microsoft.com/playwright:v1.48.0-jammy}"
BASE_URL="${BASE_URL:-http://web-frontend:80}"
PW_VER="${PW_VER:-1.48.0}"

docker run --rm --network "$NET" \
  -v "$GATE_DIR":/gate -w /gate \
  -v sentinel-pw-modules:/opt/pw \
  -e BASE_URL="$BASE_URL" \
  -e NODE_PATH=/opt/pw/lib/node_modules \
  "$IMG" bash -lc '
    export PATH=/opt/pw/bin:$PATH
    if [ ! -d /opt/pw/lib/node_modules/@playwright/test ]; then
      echo "[runner] installing @playwright/test@'"$PW_VER"' (one-time, cached volume)..."
      npm install -g --prefix /opt/pw @playwright/test@'"$PW_VER"' --no-fund --no-audit >/tmp/pwinstall.log 2>&1 \
        || { echo "[runner] npm install failed:"; cat /tmp/pwinstall.log; exit 1; }
    fi
    exec playwright test "$@"
  ' _ "$@"
