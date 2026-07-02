#!/usr/bin/env bash
# D. 링크 degraded: 링크 발급 실패 시에도 발송 계속
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

# ⚠️ 매우 침습적: web-backend 링크 API를 불능화해야 함 (web-backend 정지 시 연락처 조회도 죽어
#   전제 자체가 깨짐 — 실질적으로는 링크 라우트만 실패시킬 수단 필요. 스테이징에서 수행 권장).
echo "이 단언은 링크 API만 선택적으로 실패시킬 수단이 현 스택에 없어 자동화 불가."
echo "MANUAL: 스테이징에서 web-backend 링크 라우트 차단 후 유효 이벤트 주입 → degraded 로그 + 채널 시도 관측"
exit 2
