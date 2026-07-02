#!/usr/bin/env bash
# I. resolved 종착성 — resolved에 acknowledge 409, 재resolve 409, 공백 notes 400
# spec: docs/spec/web-backend.md — 검증 단언 (TDD)
# SKIP: mutating — 실제 incident 상태 변경 시도 (구현 오류 시 실사고 상태 오염).
set -uo pipefail; . "$(dirname "$0")/../lib-web.sh"
require_mutating
T=$(get_token) || exit 1
id=$(db_query "SELECT id FROM incidents WHERE site_id='spectdd' AND status='resolved' ORDER BY id DESC LIMIT 1")
[ -n "$id" ] || skip "(fixture 부재): spectdd resolved incident 없음 — g/j 선행 필요"
a=$(bcode -X PATCH -H "Authorization: Bearer $T" "$BACKEND/api/incidents/$id/acknowledge")
r=$(bcode -X PATCH -H "Authorization: Bearer $T" -H 'Content-Type: application/json' -d '{"resolutionNotes":"dup"}' "$BACKEND/api/incidents/$id/resolve")
oid=$(db_query "SELECT id FROM incidents WHERE site_id='spectdd' AND status='open' ORDER BY id DESC LIMIT 1")
e="skip"; [ -n "$oid" ] && e=$(bcode -X PATCH -H "Authorization: Bearer $T" -H 'Content-Type: application/json' -d '{"resolutionNotes":""}' "$BACKEND/api/incidents/$oid/resolve")
echo "ack=$a re-resolve=$r empty-notes=$e"
[ "$a" = "409" ] && [ "$r" = "409" ] && { [ "$e" = "400" ] || [ "$e" = "skip" ]; } && ok "종착성 일치" || nok "불일치"
