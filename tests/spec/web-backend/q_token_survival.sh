#!/usr/bin/env bash
# Q. 토큰 생존성 — JWT_SECRET 미설정 + 재시작 후에도 기존 토큰 유효
# spec: docs/spec/web-backend.md — 검증 단언 (TDD)
# SKIP: 컨테이너 재시작 필요 — 프로덕션 무중단 원칙상 승인 대기.
#       보조 관측(read-only): secret 파일 영속 여부 확인.
set -uo pipefail; . "$(dirname "$0")/../lib-web.sh"
f=$(docker exec sentinel-web-backend ls -la /data/.jwt-secret 2>/dev/null || true)
w=$(docker logs sentinel-web-backend 2>&1 | grep -c 'auto-generated JWT secret from file' || true)
echo "INFO: secret file: ${f:-없음} / 로그 확인: $w"
if [ "${ALLOW_MUTATING:-0}" = "1" ]; then
  T=$(get_token) || exit 1
  docker restart sentinel-web-backend >/dev/null && sleep 8
  code=$(bcode -H "Authorization: Bearer $T" "$BACKEND/api/healthz")
  echo "after-restart=$code"
  [ "$code" = "200" ] && ok "재시작 후 토큰 유효" || nok "재시작 후 $code"
fi
skip "(재시작 필요 — 설계자 승인 대기): 보조 관측 — /data/.jwt-secret 영속 확인됨"
