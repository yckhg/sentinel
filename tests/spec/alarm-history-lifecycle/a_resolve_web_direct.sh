#!/usr/bin/env bash
# A. open 경보 직접 해소(선행 acknowledge 없이) — PATCH /api/incidents/{id}/resolve
#    (유효 노트) → 200, status=resolved, resolvedByKind=web.
# spec: docs/spec/alarm-history-lifecycle.md — 단언 A (핵심, mutating·terminal)
# 기본 SKIP: 격리 스택 + ADMIN 토큰 필요. 새 open incident 를 시딩해 종단 전이시키므로
#            공유 DB 오염 방지를 위해 ALLOW_MUTATING=1 없이는 실행하지 않는다.
set -uo pipefail; . "$(dirname "$0")/../lib-web.sh"
require_mutating
T=$(get_token) || skip "(fixture 부재): admin 토큰 획득 실패"
SID="ahl-a-$(date +%s%N)-$$-${RANDOM}"
id=$(bcurl -X POST -H 'Content-Type: application/json' \
  -d "{\"siteId\":\"$SID\",\"description\":\"a-open\",\"isTest\":true}" \
  "$BACKEND/api/incidents" | jq -r .id)
[ -n "$id" ] && [ "$id" != "null" ] || nok "seed open incident 실패 (id=$id)"
out=$(bcurl_code -X PATCH -H "Authorization: Bearer $T" -H 'Content-Type: application/json' \
  -d '{"resolutionNotes":"현장 확인 완료"}' "$BACKEND/api/incidents/$id/resolve")
code=$(printf '%s' "$out" | tail -n1); body=$(printf '%s' "$out" | sed '$d')
echo "id=$id code=$code body=$body"
[ "$code" = "200" ] && printf '%s' "$body" | jq -e '.status=="resolved" and .resolvedByKind=="web"' >/dev/null \
  && ok "open → resolved(web) 직접 해소" || nok "code=$code body=$body"
