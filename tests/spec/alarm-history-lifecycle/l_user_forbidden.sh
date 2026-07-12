#!/usr/bin/env bash
# L. user 토큰으로 PATCH /api/incidents/{id}/resolve → 403 (해소는 admin 전용).
#    조회 GET /api/incidents 는 user 200.
# spec: docs/spec/alarm-history-lifecycle.md — 단언 L
# USER_TOKEN(비-admin) fixture 필요 → 없으면 SKIP. resolve 는 403 이 기대라 실제 상태변경 없음
# (권한거부가 먼저) — read-only 성격이나 mutating 라우트라 방어적으로 임의 id 사용.
set -uo pipefail; . "$(dirname "$0")/../lib-web.sh"
UT="${USER_TOKEN:-}"
[ -n "$UT" ] || skip "(fixture 부재): 비-admin USER_TOKEN 미설정 — user 권한 판정 불가"
gcode=$(bcode -H "Authorization: Bearer $UT" "$BACKEND/api/incidents?limit=1")
# id 는 존재 여부와 무관: admin 이 아니면 권한검사에서 403 이 먼저 반환되어야 한다.
rcode=$(bcode -X PATCH -H "Authorization: Bearer $UT" -H 'Content-Type: application/json' \
  -d '{"resolutionNotes":"x"}' "$BACKEND/api/incidents/999999999/resolve")
echo "list=$gcode resolve=$rcode"
[ "$gcode" = "200" ] && [ "$rcode" = "403" ] && ok "user 조회 200 · 해소 403" || nok "list=$gcode resolve=$rcode"
