#!/usr/bin/env bash
# 계약8-4. GET /api/archives 응답 각 원소 status ∈ {protecting,pending,finalizing,processing,completed,failed}.
#   (enum 정의 SSOT=recording 스펙; 본 계약은 값 소비 의무를 판정 — web-backend 는 투명 프록시)
# spec: docs/spec/interface-web-api.md 계약 8 (아카이브 status 소비자 계약)
# read-only(GET). 원소 수 n 보고(verifier 가 n==0 시 SKIPPED 오버레이). recording 다운 시 502 → skip.
set -uo pipefail; . "$(dirname "$0")/../lib-web.sh"
T=$(get_token) || skip "(fixture 부재): admin 토큰 획득 실패"
out=$(bcurl_code -H "Authorization: Bearer $T" "$BACKEND/api/archives")
code=$(echo "$out" | tail -1); body=$(echo "$out" | head -n -1)
echo "code=$code"
[ "$code" = "502" ] && skip "(하위 서비스): recording 프록시 502 — 아카이브 조회 불가"
[ "$code" = "200" ] || nok "GET /api/archives != 200 ($code)"
echo "$body" | jq -e 'type=="array"' >/dev/null || nok "응답이 배열 아님"
n=$(echo "$body" | jq 'length')
echo "INFO: archives 원소 수 n=$n (n==0 이면 enum 단언 vacuous — verifier SKIPPED 오버레이 대상)"
echo "$body" | jq -e 'all(.[]; .status | IN("protecting","pending","finalizing","processing","completed","failed"))' >/dev/null \
  || nok "enum 밖 status 존재 (소비자 계약 위반 — 미지 status 노출)"
[ "$n" = "0" ] && skip "(no-data): archives 200·배열 OK 이나 원소 0건 — enum 단언 vacuous"
ok "archives status enum 전원소 준수 (n=$n)"
