#!/usr/bin/env bash
# B. 불량 입력 거절: siteId 누락 → 400 + 채널 발송 시도 없음
# spec: docs/spec/notifier.md — 검증 단언 (TDD)
set -uo pipefail
db_query() {
  docker run --rm -v sentinel_db-data:/data:ro alpine:3.19 \
    sh -c 'apk add -q sqlite >/dev/null && sqlite3 -readonly /data/sentinel.db "$1"' sh "$1"
}
# Docker 내부망에서 notifier로 직접 주입 (curl은 일회성 컨테이너에 설치 — 호스트 미오염)
ncurl() {
  docker run --rm --network sentinel_sentinel-net alpine:3.19 sh -c \
    "apk add -q curl >/dev/null && curl $*"
}
# SKIP: mutating — 프로덕션 실행 금지 (실제 이메일 발송/시스템 알람 시도/녹화 보호 요청 유발).
if [ "${ALLOW_MUTATING:-0}" != "1" ]; then
  echo "SKIPPED (mutating — 설계자 승인 대기): ALLOW_MUTATING=1 로만 실행"
  exit 2
fi

# 주의: 검증 로직이 스펙과 다르면(NOK 케이스) 실제 발송이 일어날 수 있어 mutating으로 분류.
# 오염 배제 전략: 시간창(1초 해상도 SINCE)은 순차 실행 시 직전 valid-event(A)의 async dispatch
# 로그를 창에 섞어 false-NOK 를 낸다. 채널 로그는 deviceId 를 안 담으므로 시간창으로 이 이벤트의
# 효과만 분리하기 어렵다. 대신 **고유 deviceId 마커**로 페이로드를 태그한다:
#  - siteId 누락 → 400 으로 검증 단계에서 거절되면 dispatch 진입 로그("Received alert ... device=마커")
#    가 절대 생기지 않는다. 이 마커는 accept 된 이벤트의 진입 로그에만 나타나므로, 전체 로그에서
#    마커를 grep 하면 시간창/타 이벤트 오염 없이 "이 이벤트가 dispatch 로 진입했는가"만 정확히 관측된다.
#  - 채널 발송 시도는 accept 된 이벤트에서만 발생하므로, 마커의 dispatch 진입 부재 = 이 이벤트에
#    기인한 채널 시도 부재. (검증이 회귀해 400 대신 accept 되면 device=마커 진입 로그가 떠 NOK.)
MARKER="BADIN-$(date -u +%s)-$RANDOM"
code=$(ncurl "-s -o /dev/null -w \"%{http_code}\" -X POST http://notifier:8080/api/notify \
  -H \"Content-Type: application/json\" \
  -d \"{\\\"deviceId\\\":\\\"$MARKER\\\",\\\"type\\\":\\\"gas_leak\\\",\\\"timestamp\\\":\\\"2026-07-02T00:00:00Z\\\"}\"")
sleep 3
# 고유 마커로 전체 로그 필터 — 시간창 불필요, 이전 테스트 오염 배제.
entered=$(docker logs sentinel-notifier 2>&1 | grep -c "device=$MARKER")
echo "code=$code marker=$MARKER dispatch진입로그=$entered"
[ "$code" = 400 ] && [ "$entered" = 0 ] && { echo OK; exit 0; } || { echo NOK; exit 1; }
