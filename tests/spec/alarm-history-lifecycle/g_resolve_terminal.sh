#!/usr/bin/env bash
# G. resolve 종단성 — 같은 경보에 resolve 재호출 → 409.
# spec: docs/spec/alarm-history-lifecycle.md — 단언 G (핵심, mutating)
# 기본 SKIP: 새 open incident 를 시딩해 resolved 로 종단 전이시킨다(공유 DB 오염).
set -uo pipefail; . "$(dirname "$0")/../lib-web.sh"
require_mutating
T=$(get_token) || skip "(fixture 부재): admin 토큰 획득 실패"
SID="ahl-g-$(date +%s%N)-$$-${RANDOM}"
id=$(bcurl -X POST -H 'Content-Type: application/json' \
  -d "{\"siteId\":\"$SID\",\"description\":\"g-open\",\"isTest\":true}" \
  "$BACKEND/api/incidents" | jq -r .id)
[ -n "$id" ] && [ "$id" != "null" ] || nok "seed open incident 실패 (id=$id)"
c1=$(bcode -X PATCH -H "Authorization: Bearer $T" -H 'Content-Type: application/json' \
  -d '{"resolutionNotes":"first"}' "$BACKEND/api/incidents/$id/resolve")
c2=$(bcode -X PATCH -H "Authorization: Bearer $T" -H 'Content-Type: application/json' \
  -d '{"resolutionNotes":"second"}' "$BACKEND/api/incidents/$id/resolve")
echo "first=$c1 second=$c2"
[ "$c1" = "200" ] && [ "$c2" = "409" ] && ok "resolved 종단(재해소 409)" || nok "first=$c1 second=$c2"
