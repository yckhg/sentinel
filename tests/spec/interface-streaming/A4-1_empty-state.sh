#!/usr/bin/env bash
# SKIP: mutating — "push가 하나도 없는 초기 상태"는 프로덕션에서 컨테이너 재생성 또는
#   어댑터 중지가 필요 (설계자 승인 대기). 현재 프로덕션에는 활성 push 2개가 존재.
# A4-1 (빈 상태): push 없는 초기 상태에서 GET /api/streams → [] 이면 OK.
set -u

if [ "${SPEC_TDD_ALLOW_MUTATING:-0}" != "1" ]; then
  echo "SKIPPED: requires zero-push state (container recreate / adapter stop). Set SPEC_TDD_ALLOW_MUTATING=1 to run."
  exit 2
fi

# 승인 시 실행 절차: streaming 재생성 직후(어댑터 push 재개 전) 즉시 조회
COMPOSE_PROJECT_NAME=sentinel docker compose -f "$(git rev-parse --show-toplevel)/docker-compose.yml" up -d --force-recreate streaming
sleep 3
BODY=$(docker exec sentinel-cctv-adapter wget -qO- http://streaming:8080/api/streams)
echo "body: $BODY"
if [ "$BODY" = "[]" ]; then echo "OK"; exit 0; fi
echo "NOK: expected [] got: $BODY"
exit 1
