#!/usr/bin/env bash
# Shared helpers — spec TDD for docs/spec/archive-download-ux.md (단위 A/B, 라이브 API).
# PRODUCTION-SAFE: 기본 read-only observation only (GET, wget, ffprobe).
# Mutating steps (아카이브 생성 POST) are gated behind ALLOW_MUTATING=1 /
# SPEC_TDD_ALLOW_MUTATING=1 (designer approval required — do NOT set on production).
#
# green-wash 금지: 대상 상태 fixture(미완료/completed/failed 아카이브)가 없으면
# OK가 아니라 "판정 불가(전제 미충족)" 또는 명시적 SKIP으로 처리한다.
set -u

REC_CONTAINER="${REC_CONTAINER:-sentinel-recording}"
REC="${REC:-http://localhost:8080}"
ALLOW_MUTATING="${ALLOW_MUTATING:-${SPEC_TDD_ALLOW_MUTATING:-0}}"
FAILED=0

# Run a shell command inside the recording container (read-only usage only).
rexec() { docker exec "$REC_CONTAINER" sh -c "$1"; }

# http_head <url> — sets STATUS and CTYPE from busybox wget (headers on stderr).
http_head() {
  local url="$1" hdr
  hdr="$(docker exec "$REC_CONTAINER" sh -c "wget -S -q -O /dev/null '$url'" 2>&1)" || true
  STATUS="$(printf '%s\n' "$hdr" | grep -oE 'HTTP/[0-9.]+ [0-9]{3}' | tail -1 | awk '{print $2}')"
  CTYPE="$(printf '%s\n' "$hdr" | grep -i 'content-type:' | tail -1 | sed 's/.*[Cc]ontent-[Tt]ype: *//' | tr -d '\r')"
}

# archives_json <hostfile> — dumps GET /api/archives body to a host temp file.
archives_json() { rexec "wget -qO- $REC/api/archives" > "$1"; }

# ids_with_status <jsonfile> <status> — prints matching archive ids (one per line).
ids_with_status() {
  python3 -c "
import json,sys
data=json.load(open('$1'))
for x in data:
    if x.get('status')=='$2': print(x['id'])
" 2>/dev/null
}

ok()   { echo "  [ok]  $*"; }
nok()  { echo "  [NOK] $*"; FAILED=1; }
info() { echo "  [..]  $*"; }

verdict() {
  if [ "$FAILED" -eq 0 ]; then echo "VERDICT $1: OK"; else echo "VERDICT $1: NOK"; exit 1; fi
}

# SKIP reasons — each keeps the wording explicit per spec §검증 스킵 선언.
skip_mutating() { echo "VERDICT $1: SKIPPED (mutating — 설계자 승인 대기, ALLOW_MUTATING=1 필요) — $2"; exit 0; }
skip_staging()  { echo "VERDICT $1: SKIPPED (staging recorder 미확보 — INDEX §SKIP조건 5: 더미 RTMP + 격리 아카이브 볼륨) — $2"; exit 0; }
skip_delta()    { echo "VERDICT $1: SKIPPED (completedAt 델타 recording 미착지) — $2"; exit 0; }
skip_browser()  { echo "VERDICT $1: SKIPPED (needs-browser — Playwright 세션 필요) — $2"; exit 0; }

# require_container — SKIP-guard if the recording service isn't reachable.
require_container() {
  if ! docker inspect "$REC_CONTAINER" >/dev/null 2>&1; then
    echo "VERDICT $1: SKIPPED (recording 컨테이너 '$REC_CONTAINER' 미기동 — 라이브 스택 부재)"; exit 0
  fi
}
