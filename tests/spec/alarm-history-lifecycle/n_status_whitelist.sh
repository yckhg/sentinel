#!/usr/bin/env bash
# N. status 필터 화이트리스트 — ?status=acknowledged (및 {open,resolved} 밖 임의 값) → 400.
#    ?status=open · ?status=resolved → 200.
# spec: docs/spec/alarm-history-lifecycle.md — 단언 N (라이브 확인; 백엔드 유닛게이트와 이중)
# read-only.
set -uo pipefail; . "$(dirname "$0")/../lib-web.sh"
T=$(get_token) || skip "(fixture 부재): admin 토큰 획득 실패"
ca=$(bcode -H "Authorization: Bearer $T" "$BACKEND/api/incidents?status=acknowledged")
cb=$(bcode -H "Authorization: Bearer $T" "$BACKEND/api/incidents?status=bogus")
co=$(bcode -H "Authorization: Bearer $T" "$BACKEND/api/incidents?status=open")
cr=$(bcode -H "Authorization: Bearer $T" "$BACKEND/api/incidents?status=resolved")
echo "acknowledged=$ca bogus=$cb open=$co resolved=$cr"
[ "$ca" = "400" ] && [ "$cb" = "400" ] && [ "$co" = "200" ] && [ "$cr" = "200" ] \
  && ok "status 화이트리스트 {open,resolved}" || nok "acknowledged=$ca bogus=$cb open=$co resolved=$cr"
