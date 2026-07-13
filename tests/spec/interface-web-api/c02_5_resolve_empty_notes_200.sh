#!/usr/bin/env bash
# 계약2-5. resolutionNotes:"" resolve → 200 (해제 노트 선택 — 빈 노트 허용)
# spec: docs/spec/interface-web-api.md 계약 2 · docs/spec/alarm-history-lifecycle.md 단언 B
# SKIP: mutating — 실제 open incident 를 resolved 로 종단 전이(빈 노트라도 200 성공 = 실사고 해소).
set -uo pipefail; . "$(dirname "$0")/../lib-web.sh"
require_mutating
T=$(get_token) || exit 1
id=$(bcurl -H "Authorization: Bearer $T" "$BACKEND/api/incidents?status=open&limit=1" | jq -r '.data[0].id // empty')
[ -n "$id" ] || skip "(fixture 부재): open incident 없음"
code=$(bcode -X PATCH -H "Authorization: Bearer $T" -H 'Content-Type: application/json' \
  -d '{"resolutionNotes":""}' "$BACKEND/api/incidents/$id/resolve")
echo "code=$code"
[ "$code" = "200" ] && ok "빈 notes 200 (노트 선택)" || nok "기대 200, 관측 $code"
