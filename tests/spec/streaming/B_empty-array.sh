#!/usr/bin/env bash
# SKIP: mutating — "발행 중인 스트림이 없는 상태(컨테이너 재생성 직후)"를 만들려면
#   프로덕션 streaming 컨테이너 재생성이 필요 (설계자 승인 대기).
# streaming §단언 B (무스트림 빈 배열): 재생성 직후 GET /api/streams 가 200 + 정확히 [].
set -u

if [ "${SPEC_TDD_ALLOW_MUTATING:-0}" != "1" ]; then
  echo "SKIPPED: requires streaming container recreate. Set SPEC_TDD_ALLOW_MUTATING=1 to run."
  exit 2
fi

docker compose -f /home/yc/projects/sentinel/docker-compose.yml up -d --force-recreate streaming
sleep 3
BODY=$(docker exec sentinel-cctv-adapter wget -qO- http://streaming:8080/api/streams)
echo "body: $BODY"
[ "$BODY" = "[]" ] && { echo "OK"; exit 0; }
echo "NOK: expected [] got: $BODY"
exit 1
