#!/usr/bin/env bash
# I. resolved 종착성 — acknowledge 라우트 부재 404(엔드포인트 제거), 재resolve 409, 공백 notes 200(노트 선택)
#   상태기계 {open,acknowledged,resolved}→{open,resolved} 축소·노트 선택화 반영.
#   acknowledge 액션/라우트가 계약에서 제거되어 예전 "resolved에 ack → 409"는 성립 불가(라우트 부재 404).
#   공백 notes 도 400 아닌 200 으로 해소된다.
# spec: docs/spec/alarm-history-lifecycle.md 단언 C·G·B (신규 계약) · docs/spec/web-backend.md 생명주기
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
{ [ "$a" = "404" ] || [ "$a" = "405" ]; } && [ "$r" = "409" ] && { [ "$e" = "200" ] || [ "$e" = "skip" ]; } && ok "종착성 일치 (ack 라우트 부재·재해소 409·빈노트 200)" || nok "불일치"
