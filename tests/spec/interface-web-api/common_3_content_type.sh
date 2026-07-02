#!/usr/bin/env bash
# 공통-3. JSON 응답 Content-Type=application/json, 단 두 /healthz는 text/plain (실측 계약)
# spec: docs/spec/interface-web-api.md — 공통 검증 단언
set -uo pipefail; . "$(dirname "$0")/../lib-web.sh"
T=$(get_token) || skip "(fixture 부재): admin 토큰 획득 실패"
h1=$(bhead "$BACKEND/healthz" | grep -i '^content-type' | tr -d '\r')
h2=$(bhead "$HWGW/healthz" | grep -i '^content-type' | tr -d '\r')
j1=$(bhead -H "Authorization: Bearer $T" "$BACKEND/api/health" | grep -i '^content-type' | tr -d '\r')
j2=$(bhead "$BACKEND/api/cameras" | grep -i '^content-type' | tr -d '\r')   # 401 에러 봉투도 JSON
echo "web-backend /healthz: $h1"; echo "hw-gateway /healthz: $h2"
echo "/api/health: $j1"; echo "/api/cameras(401): $j2"
echo "$h1" | grep -qi 'text/plain' || nok "web-backend healthz가 text/plain 아님: $h1"
echo "$h2" | grep -qi 'text/plain' || nok "hw-gateway healthz가 text/plain 아님: $h2"
echo "$j1" | grep -qi 'application/json' || nok "/api/health JSON 아님: $j1"
echo "$j2" | grep -qi 'application/json' || nok "401 에러 봉투 JSON 아님: $j2"
ok "Content-Type 규약 일치 (healthz=text/plain, JSON=application/json)"
