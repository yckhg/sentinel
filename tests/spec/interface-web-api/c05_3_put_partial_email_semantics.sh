#!/usr/bin/env bash
# 계약5-3. {"name":"새이름"}만 PUT → 200, phone 유지, email=="" (email은 partial 아님 — 실측 계약)
# spec: docs/spec/interface-web-api.md 계약 5 (⚠️ 리뷰 항목 4)
# SKIP: mutating — 실제 연락처(비상연락망)의 email이 NULL로 삭제됨. 절대 무단 실행 금지.
set -uo pipefail; . "$(dirname "$0")/../lib-web.sh"
require_mutating
T=$(get_token) || exit 1
# 반드시 spec-tdd가 만든 연락처로만 수행하도록 설계돼 있음 (기존 연락처 보호)
id=$(bcurl -X POST -H "Authorization: Bearer $T" -H 'Content-Type: application/json' \
  -d '{"name":"spectdd-c","phone":"010-1234-5678","email":"spectdd@example.com","notifyEmail":false}' "$BACKEND/api/contacts" | jq -r .id)
out=$(bcurl -X PUT -H "Authorization: Bearer $T" -H 'Content-Type: application/json' \
  -d '{"name":"spectdd-c2"}' "$BACKEND/api/contacts/$id")
bcurl -X DELETE -H "Authorization: Bearer $T" "$BACKEND/api/contacts/$id" >/dev/null  # cleanup
echo "$out" | jq -c .
echo "$out" | jq -e '.phone=="010-1234-5678" and (.email=="" or .email==null)' >/dev/null \
  && ok "phone 유지 + email 덮어쓰기(실측 시맨틱)" || nok "시맨틱 불일치"
