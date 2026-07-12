#!/usr/bin/env bash
# F. GET /api/incidents?status=resolved → data[] 전원 resolved · ?status=open → 전원 open.
# spec: docs/spec/alarm-history-lifecycle.md — 단언 F
# read-only.
set -uo pipefail; . "$(dirname "$0")/../lib-web.sh"
T=$(get_token) || skip "(fixture 부재): admin 토큰 획득 실패"
rbody=$(bcurl -H "Authorization: Bearer $T" "$BACKEND/api/incidents?status=resolved&limit=100")
obody=$(bcurl -H "Authorization: Bearer $T" "$BACKEND/api/incidents?status=open&limit=100")
rn=$(printf '%s' "$rbody" | jq '.data|length' 2>/dev/null)
on=$(printf '%s' "$obody" | jq '.data|length' 2>/dev/null)
echo "resolved=$rn open=$on"
[ -n "$rn" ] && [ -n "$on" ] || nok "응답 파싱 실패"
printf '%s' "$rbody" | jq -e 'all(.data[]; .status=="resolved")' >/dev/null || nok "?status=resolved 에 비-resolved 존재"
printf '%s' "$obody" | jq -e 'all(.data[]; .status=="open")' >/dev/null || nok "?status=open 에 비-open 존재"
ok "status 필터 분리 정확"
