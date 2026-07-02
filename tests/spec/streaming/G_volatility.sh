#!/usr/bin/env bash
# SKIP: mutating — 프로덕션 streaming 컨테이너 재생성(--force-recreate)이 필요
#   (설계자 승인 대기). 실행 중 스트림 전체가 끊기는 파괴적 동작.
# streaming §단언 G (휘발성): 재생성 직후 /api/streams 가 [] (이전 스트림 잔재 없음).
set -u

if [ "${SPEC_TDD_ALLOW_MUTATING:-0}" != "1" ]; then
  echo "SKIPPED: requires container recreate of production streaming. Set SPEC_TDD_ALLOW_MUTATING=1 to run."
  exit 2
fi

docker compose -f /home/yc/projects/sentinel/docker-compose.yml up -d --force-recreate streaming
sleep 3
BODY=$(docker exec sentinel-cctv-adapter wget -qO- http://streaming:8080/api/streams)
echo "body: $BODY"
[ "$BODY" = "[]" ] && { echo "OK: no residue after recreate"; exit 0; }
echo "NOK: residue found after recreate: $BODY"
exit 1
