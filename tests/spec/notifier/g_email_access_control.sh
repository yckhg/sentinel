#!/usr/bin/env bash
# G. 이메일 API 접근 통제: 외부 IP 403 + script 태그 sanitize
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

# 1) 외부 IP 403: 내부망 컨테이너에서는 시뮬레이션 불가 — 공인 IP 발신점 필요 (MANUAL).
# 2) sanitize: 실제 메일 발송이 일어나며 수신함 관측 필요.
# 전제 미충족(TEST_EMAIL 미설정)은 실패(NOK)가 아니라 SKIPPED(exit2) — pass/fail 오염 방지.
if [ -z "${TEST_EMAIL:-}" ]; then
  echo "SKIPPED (전제 미충족): TEST_EMAIL(수신 확인 가능한 주소) 미설정 — 403/sanitize 단언 검증 불가"
  exit 2
fi
ncurl "-s -w \"|%{http_code}\" -X POST http://notifier:8080/api/send-email -H \"Content-Type: application/json\" \
  -d \"{\\\"to\\\":\\\"$TEST_EMAIL\\\",\\\"subject\\\":\\\"spec G sanitize\\\",\\\"body\\\":\\\"<p>hi</p><script>alert(1)</script>\\\"}\""
echo
echo "MANUAL: 수신 메일 본문에 <script> 부재 확인 + 외부 IP에서 403 확인은 별도 발신점 필요"
exit 2
