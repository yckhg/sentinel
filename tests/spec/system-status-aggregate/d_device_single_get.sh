#!/usr/bin/env bash
# D. 검색 — 계약 6 단건 조회 GET /api/devices/{id} (수치 DB id) 델타.
#    정상 장비도 지목 조회되며, 미등록/삭제 id 는 404(계약 6 규약).
# spec: docs/spec/system-status-aggregate.md — 단언 D / 계약 6 델타
# read-only. 기존 등록 장비를 fixture 로 사용.
set -uo pipefail; . "$(dirname "$0")/../lib-web.sh"
T=$(get_token) || skip "(fixture 부재): admin 토큰 획득 실패"

id=$(bcurl -H "Authorization: Bearer $T" "$BACKEND/api/devices" | jq -r 'map(.id) | first // empty')
[ -n "$id" ] || skip "(fixture 부재): 등록된 장비 없음"
echo "target device id=$id"

# 단건 GET — device 오브젝트(lastSeen/alertState 포함) 반환
out=$(bcurl_code -H "Authorization: Bearer $T" "$BACKEND/api/devices/$id")
code=$(printf '%s' "$out" | tail -n1); body=$(printf '%s' "$out" | sed '$d')
echo "single code=$code"; printf '%s' "$body" | jq -c '{id, siteId, deviceId, lastSeen, alertState}' 2>/dev/null | head -c 300; echo
[ "$code" = "200" ] || nok "GET /api/devices/$id 기대 200, 관측 $code (계약 6 단건 GET 델타 미구현?)"
printf '%s' "$body" | jq -e --argjson id "$id" '.id==$id and has("lastSeen") and has("alertState")' >/dev/null \
  || nok "단건 응답이 device 오브젝트(id/lastSeen/alertState) 아님"

# 미등록 id → 404 (리소스-부재)
miss=$(bcode -H "Authorization: Bearer $T" "$BACKEND/api/devices/999999999")
echo "missing code=$miss"
[ "$miss" = "404" ] || nok "미등록 장비 조회 기대 404, 관측 $miss"

ok "단건 조회 200(device 오브젝트) + 미등록 404"
