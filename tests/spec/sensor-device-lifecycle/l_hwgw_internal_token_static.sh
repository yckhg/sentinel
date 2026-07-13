#!/usr/bin/env bash
# L (핵심, static — 비-mutating) — hw-gateway 헤더 동봉.
#   hw-gateway가 web-backend로 보내는 POST /api/devices/seen (postDeviceSeen)·
#   POST /api/incidents (forwardToWebBackend) 두 송신부 모두에 X-Internal-Token 헤더를
#   설정함을 정적 확인한다. fail-closed 게이트의 폭발 반경(누락 시 프로덕션 heartbeat
#   seen·위기 자동등록이 전면 401)을 상시 정적 게이트로 못박는다. D·E·F·E2·I·I2 런타임
#   단언은 하네스가 토큰을 직접 주입하므로 이 송신부 누락을 가리지 못한다(그 사각을 L이 메운다).
# spec: docs/spec/sensor-device-lifecycle.md — 검증 단언 L · §의존 도구(내부 신뢰 경계)
#
# 순수 정적(소스 grep, 네트워크·mutating 없음) → ALLOW_MUTATING 없이 상시 실행.
# 각 함수 본문을 격리해 확인한다(resolve-from-sensor 등 다른 POST 송신부와의 단순
# 개수 카운트 혼동 금지).
set -uo pipefail

SRC="$(cd "$(dirname "$0")/../../.." && pwd)/services/hw-gateway/main.go"
FAIL=0
nok() { echo "NOK: $*"; FAIL=1; }
ok()  { echo "OK: $*"; }

[ -f "$SRC" ] || { echo "NOK: hw-gateway main.go not found at $SRC"; exit 1; }

# fn_body <name> — print the body of the top-level `func <name>(` up to the next
# top-level `func ` (column-0). Isolates the target so the header check cannot leak
# from a neighbouring sender.
fn_body() {
  awk -v fn="func $1(" '
    index($0, fn)==1 { inbody=1 }
    inbody && NR>startline && /^func / && index($0, fn)!=1 { if (seen) exit }
    inbody { print; seen=1 }
    inbody && /^func / && index($0, fn)==1 { startline=NR }
  ' "$SRC"
}

check() {
  local fn="$1" endpoint="$2"
  local body; body="$(fn_body "$fn")"
  if [ -z "$body" ]; then nok "$fn not found in hw-gateway main.go"; return; fi
  if printf '%s' "$body" | grep -qE 'req\.Header\.Set\("X-Internal-Token"'; then
    ok "$fn ($endpoint) sets X-Internal-Token"
  else
    nok "$fn ($endpoint) is MISSING req.Header.Set(\"X-Internal-Token\") — fail-closed gate would 401 all traffic"
  fi
}

check postDeviceSeen      "POST /api/devices/seen"
check forwardToWebBackend "POST /api/incidents"

if [ "$FAIL" = 0 ]; then echo "L: OK"; exit 0; else echo "L: NOK"; exit 1; fi
