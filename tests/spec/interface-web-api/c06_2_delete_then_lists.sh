#!/usr/bin/env bash
# 계약6-2. DELETE 후 /api/devices 미포함, /api/devices/all에 deletedAt 채워져 포함
# spec: docs/spec/interface-web-api.md 계약 6
# SKIP: mutating — 실제 장비를 soft-delete 함 (헬스 감시/재시작 대상에서 제외됨).
set -uo pipefail; . "$(dirname "$0")/../lib-web.sh"
require_mutating
T=$(get_token) || exit 1
# spec-tdd 전용 장비를 seen으로 만든 뒤 삭제 (기존 장비 보호)
bcurl -X POST -H 'Content-Type: application/json' -d '{"siteId":"spectdd","deviceId":"SPEC-DEL-01"}' "$BACKEND/api/devices/seen" >/dev/null
id=$(bcurl -H "Authorization: Bearer $T" "$BACKEND/api/devices" | jq -r '.[] | select(.deviceId=="SPEC-DEL-01") | .id')
[ -n "$id" ] || nok "테스트 장비 생성 실패"
bcurl -X DELETE -H "Authorization: Bearer $T" "$BACKEND/api/devices/$id" >/dev/null
in_list=$(bcurl -H "Authorization: Bearer $T" "$BACKEND/api/devices" | jq "[.[] | select(.id==$id)] | length")
del_at=$(bcurl -H "Authorization: Bearer $T" "$BACKEND/api/devices/all" | jq -r ".[] | select(.id==$id) | .deletedAt")
echo "in_list=$in_list deletedAt=$del_at"
[ "$in_list" = "0" ] && [ -n "$del_at" ] && [ "$del_at" != "null" ] && ok "soft-delete 반영" || nok "목록 시맨틱 불일치"
