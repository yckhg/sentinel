#!/usr/bin/env bash
# 계약14-1. 토큰 없이 /ws 업그레이드 → 401 (연결 수립 안 됨)
# spec: docs/spec/interface-web-api.md 계약 14
set -uo pipefail; . "$(dirname "$0")/../lib-web.sh"
out=$(ws_observe "/ws" 5 normal)
echo "$out"
echo "$out" | grep -q '^HTTP: HTTP/1.1 401' && ok "업그레이드 401 거절" || nok "401 아님: $(echo "$out" | head -1)"
