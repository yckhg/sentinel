#!/usr/bin/env bash
# 계약2-5. resolutionNotes:"" resolve → 400
# spec: docs/spec/interface-web-api.md 계약 2
# SKIP: mutating — 실제 incident 대상 PATCH (검증 실패 기대지만 구현 오류 시 실사고 resolve 위험).
set -uo pipefail; . "$(dirname "$0")/../lib-web.sh"
require_mutating
T=$(get_token) || exit 1
id=$(bcurl -H "Authorization: Bearer $T" "$BACKEND/api/incidents?status=open&limit=1" | jq -r '.data[0].id // empty')
[ -n "$id" ] || skip "(fixture 부재): open incident 없음"
code=$(bcode -X PATCH -H "Authorization: Bearer $T" -H 'Content-Type: application/json' \
  -d '{"resolutionNotes":""}' "$BACKEND/api/incidents/$id/resolve")
echo "code=$code"
[ "$code" = "400" ] && ok "빈 notes 400" || nok "기대 400, 관측 $code"
