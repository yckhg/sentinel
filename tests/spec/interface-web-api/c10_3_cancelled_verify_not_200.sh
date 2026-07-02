#!/usr/bin/env bash
# 계약10-3. DELETE(취소)된 초대 token verify → 400 또는 404 (200 아님)
# spec: docs/spec/interface-web-api.md 계약 10
# 실행 방식: 과거에 이미 취소된 초대 토큰을 DB에서 read-only로 취득해 GET verify만 수행.
set -uo pipefail; . "$(dirname "$0")/../lib-web.sh"
tok=$(db_query "SELECT token FROM invitations WHERE status='cancelled' LIMIT 1")
[ -n "$tok" ] || skip "(fixture 부재): cancelled 초대 없음"
out=$(bcurl_code "$BACKEND/api/invitations/verify/$tok")
code=$(echo "$out" | tail -1); body=$(echo "$out" | head -n -1)
echo "code=$code body=$body"
{ [ "$code" = "400" ] || [ "$code" = "404" ]; } && ok "취소 토큰 verify $code" || nok "기대 400/404, 관측 $code"
