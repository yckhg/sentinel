#!/usr/bin/env bash
# 계약3-9. GET /api/cameras — 비어있지 않은 hlsUrl은 /live/로 시작, http 미포함 (상대 경로 정책)
# spec: docs/spec/interface-web-api.md 계약 3 (interface-streaming A4-4 교차)
set -uo pipefail; . "$(dirname "$0")/../lib-web.sh"
T=$(get_token) || skip "(fixture 부재): admin 토큰 획득 실패"
out=$(bcurl -H "Authorization: Bearer $T" "$BACKEND/api/cameras")
echo "$out" | jq -c '[.[] | {id, streamKey, hlsUrl, status}]'
n=$(echo "$out" | jq 'length'); [ "$n" -ge 1 ] || skip "(fixture 부재): 등록 카메라 없음"
echo "$out" | jq -e 'all(.[]; .hlsUrl == "" or (.hlsUrl | startswith("/live/") and (contains("http") | not)))' >/dev/null \
  && ok "모든 hlsUrl 상대 경로 규약 준수 (${n}대)" || nok "절대 URL 또는 비정상 hlsUrl 존재"
