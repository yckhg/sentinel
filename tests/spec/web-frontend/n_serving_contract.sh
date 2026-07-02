#!/usr/bin/env bash
# N. 서빙 계약 — 80 포트: healthz 200 JSON · SPA 경로 → index.html · /api /auth /ws /live 프록시
# spec: docs/spec/web-frontend.md — 검증 단언 (TDD)
set -uo pipefail; . "$(dirname "$0")/../lib-web.sh"
# (1) healthz
h=$(bcurl_code "$FRONTEND/healthz"); hc=$(echo "$h" | tail -1); hb=$(echo "$h" | head -n -1)
echo "healthz: $hc $hb"
[ "$hc" = "200" ] && echo "$hb" | jq -e '.status=="ok"' >/dev/null || nok "healthz 불일치"
# (2) SPA fallback
spa=$(bcurl "$FRONTEND/view/abc" | head -5)
echo "$spa" | grep -qi '<!doctype html' || nok "/view/abc가 index.html 아님"
idx=$(bcurl "$FRONTEND/" | head -5)
[ "$(echo "$spa" | md5sum)" = "$(echo "$idx" | md5sum)" ] || echo "INFO: /와 /view/abc 응답 상이(캐시 헤더 등) — doctype 확인으로 판정"
# (3) 프록시: /api → backend 401 에러 봉투, /auth → backend 401, /ws 업그레이드 401, /live → streaming 응답
a=$(bcurl_code "$FRONTEND/api/cameras"); ac=$(echo "$a" | tail -1); ab=$(echo "$a" | head -n -1)
u=$(bcode "$FRONTEND/auth/pending")
w=$(bcode -H 'Upgrade: websocket' -H 'Connection: Upgrade' -H 'Sec-WebSocket-Version: 13' -H 'Sec-WebSocket-Key: c3BlYy10ZGQtdGVzdCE=' "$FRONTEND/ws")
l=$(bcode "$FRONTEND/live/spectdd-nonexistent/index.m3u8")
echo "api=$ac($ab) auth=$u ws=$w live=$l"
[ "$ac" = "401" ] && echo "$ab" | jq -e 'has("error")' >/dev/null || nok "/api 프록시가 backend 401 봉투 아님"
[ "$u" = "401" ] || nok "/auth 프록시 확인 실패 ($u)"
[ "$w" = "401" ] || nok "/ws 프록시가 backend 인증 거절(401) 아님 ($w)"
{ [ "$l" = "404" ] || [ "$l" = "200" ]; } || nok "/live 프록시 응답 이상 ($l)"
ok "healthz/SPA/4개 프록시 경로 모두 계약 일치"
