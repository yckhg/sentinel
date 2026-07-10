#!/usr/bin/env bash
# L. 비활성 채널 스킵: KAKAO_ENABLED/SMS_ENABLED != "true" 인 채널은 외부 발송 시도 로그가
#    나타나지 않고, 곧바로 다음 폴백 단계(SMS 또는 시스템 알람)로 진행한 기록이 남는다.
# spec: docs/spec/notifier.md — 검증 단언 L
# 판정: OK=exit0, NOK=exit1 (비활성 채널의 외부 발송 시도 관측), SKIPPED=exit2.
set -uo pipefail

db_query() {
  docker run --rm -v sentinel_db-data:/data:ro alpine:3.19 \
    sh -c 'apk add -q sqlite >/dev/null && sqlite3 -readonly /data/sentinel.db "$1"' sh "$1"
}
getenv() {
  docker inspect sentinel-notifier --format "{{range .Config.Env}}{{println .}}{{end}}" 2>/dev/null \
    | grep -E "^$1=" | head -1 | cut -d= -f2-
}
ncurl() {
  docker run --rm --network sentinel_sentinel-net alpine:3.19 sh -c \
    "apk add -q curl >/dev/null && curl $*"
}

KAKAO_ENABLED=$(getenv KAKAO_ENABLED)
SMS_ENABLED=$(getenv SMS_ENABLED)
kakao_off=0; sms_off=0
[ "$KAKAO_ENABLED" != "true" ] && kakao_off=1
[ "$SMS_ENABLED" != "true" ] && sms_off=1
echo "KAKAO_ENABLED=${KAKAO_ENABLED:-<unset>} SMS_ENABLED=${SMS_ENABLED:-<unset>}"

# 둘 다 활성이면 비활성 채널이 없어 단언 관측 불가.
if [ "$kakao_off" -eq 0 ] && [ "$sms_off" -eq 0 ]; then
  echo "SKIPPED (부적절, 비활성 채널 없음): 두 채널 모두 활성"
  exit 2
fi

# 유효 이벤트 주입은 실제 발송/알람을 유발 — mutating. ALLOW_MUTATING=1 게이트로만.
if [ "${ALLOW_MUTATING:-0}" != "1" ]; then
  echo "SKIPPED (mutating — 설계자 승인 대기): ALLOW_MUTATING=1 로만 실행"
  exit 2
fi

# 폴백 체인을 관측하려면 연락처 ≥1 필요.
ncontacts=$(db_query "SELECT COUNT(*) FROM contacts WHERE deleted_at IS NULL;" 2>/dev/null || db_query "SELECT COUNT(*) FROM contacts;")
[ "$ncontacts" -ge 1 ] || { echo "SKIPPED: 연락처 0건 — 폴백 체인 미실행"; exit 2; }
echo "contacts=$ncontacts"

# 순차 실행 시 직전 이벤트 로그 혼입을 막기 위해 절대 시간창 고정.
SINCE=$(date -u +%Y-%m-%dT%H:%M:%SZ)
ncurl "-s -o /dev/null -X POST http://notifier:8080/api/notify -H \"Content-Type: application/json\" \
  -d \"{\\\"siteId\\\":\\\"site1\\\",\\\"deviceId\\\":\\\"TEST-L1\\\",\\\"type\\\":\\\"gas_leak\\\",\\\"timestamp\\\":\\\"$(date -u +%Y-%m-%dT%H:%M:%SZ)\\\",\\\"test\\\":true}\""
sleep 8
log=$(docker logs sentinel-notifier --since "$SINCE" 2>&1)

rc=0
# 비활성 채널의 "외부 발송 시도" 로그 패턴 (sendKakaoTalk/sendSMS만 방출). 스킵/진행 로그는
# 시도가 아니므로 시도 패턴에서 제외한다.
if [ "$kakao_off" -eq 1 ]; then
  attempt=$(printf '%s' "$log" | grep -cE "^\[kakao\]|\[kakao\] |KakaoTalk (SUCCESS|FAILED)" || true)
  progress=$(printf '%s' "$log" | grep -cE "KakaoTalk skipped.*proceeding" || true)
  echo "kakao: 외부시도로그=$attempt 진행로그=$progress"
  { [ "$attempt" -eq 0 ] && [ "$progress" -ge 1 ]; } || { echo "NOK(kakao): 시도 관측 또는 진행로그 부재"; rc=1; }
fi
if [ "$sms_off" -eq 1 ]; then
  attempt=$(printf '%s' "$log" | grep -cE "\[sms\]|SMS (SUCCESS|FAILED)" || true)
  # 진행 판정은 명시적 skip-전이 로그를 필수로 한다. `System alarm` 라인 단독은
  # 인정하지 않는다 — 그러면 skip-전이 로그가 회귀로 사라져도 통과할 여지가 있다.
  progress=$(printf '%s' "$log" | grep -cE "SMS skipped.*proceeding to system alarm" || true)
  echo "sms: 외부시도로그=$attempt skip전이로그=$progress"
  { [ "$attempt" -eq 0 ] && [ "$progress" -ge 1 ]; } || { echo "NOK(sms): 시도 관측 또는 skip전이로그 부재"; rc=1; }
fi

[ "$rc" -eq 0 ] && { echo OK; exit 0; } || { echo NOK; exit 1; }
