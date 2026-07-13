#!/usr/bin/env bash
# 계약2-6. acknowledge 엔드포인트 제거 확인 → 라우트 부재 404 (405 도 허용)
#   상태기계 {open,acknowledged,resolved}→{open,resolved} 축소로 acknowledge 액션·라우트가
#   계약에서 제거됨. 예전 "user 토큰 403"(role 거부)은 라우트 존재를 전제했으나 이제 무의미 —
#   라우트 자체가 없어 role 판정 이전에 404 로 떨어진다. 계약대로 라우트 부재를 판정하도록 재정의.
# spec: docs/spec/alarm-history-lifecycle.md 단언 C · docs/spec/interface-web-api.md 계약 2
# SKIP: mutating(PATCH 프로브) — 라우트가 남아있다면 실 incident acknowledge 유발 가능.
set -uo pipefail; . "$(dirname "$0")/../lib-web.sh"
require_mutating
T=$(get_token) || exit 1
id=$(bcurl -H "Authorization: Bearer $T" "$BACKEND/api/incidents?status=open&limit=1" | jq -r '.data[0].id // empty')
[ -n "$id" ] || skip "(fixture 부재): open incident 없음"
code=$(bcode -X PATCH -H "Authorization: Bearer $T" "$BACKEND/api/incidents/$id/acknowledge")
echo "code=$code"
{ [ "$code" = "404" ] || [ "$code" = "405" ]; } && ok "acknowledge 라우트 부재($code)" || nok "기대 404/405, 관측 $code"
