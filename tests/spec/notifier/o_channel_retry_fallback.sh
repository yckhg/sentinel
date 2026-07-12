#!/usr/bin/env bash
# O. 외부 채널 즉시 재시도 후 폴백 (5xx/연결거부는 재시도, 타임아웃은 즉시 폴백):
#    알림톡 채널이 빠르게 판별되는 일시 오류(연결 거부·5xx)로 실패하고 CHANNEL_RETRY_MAX≥1 이면,
#    다음 폴백 단계로 강등하기 전 같은 채널로 즉시 재시도(같은 채널 2회 이상 시도)한 기록이 남고
#    이어서 SMS/시스템 알람으로 폴백한다. 재시도 총 지연 포함 채널 소요는 §출력 7 상한(12s) 이내.
# spec: docs/spec/notifier.md — 검증 단언 O (§출력 13, 지연 상한 §출력 7)
#
# 이 게이트가 단일 런에서 관측하는 것(주 판정): 연결거부(=빠른 재시도-적격 실패) + RETRY_MAX=1
#   → 같은 채널 재시도 로그 + 폴백. 단일 런에서 함께 단언하지 못하고 헤더로 문서화하는 분기:
#   - CHANNEL_RETRY_MAX=0 → 재시도 없이 즉시 폴백(현 구현도 1회만 시도하므로 비변별적).
#   - 타임아웃 실패 → 재시도 트리거 아님(§13) → 재시도 없이 §7 상한 내 폴백. 이 분기는 제어 가능한
#     '느린 응답' 엔드포인트가 필요(연결거부는 즉시라 타임아웃 재현 불가) — 아래에서 SKIP-문서화.
# 전제 조작: 라이브 스택은 채널 비활성이라 재시도 경로가 안 돈다. 이미 빌드된 이미지를 재사용해
#   격리 컨테이너에서 알림톡을 '연결 거부' 엔드포인트로 강제한다(라이브 recording 무오염).
# 판정: OK=exit0 (재시도 로그 + 폴백), NOK=exit1 (RETRY_MAX≥1 인데 재시도 없이 즉시 강등), SKIPPED=exit2.
set -uo pipefail
. "$(dirname "$0")/../lib-web.sh"

require_mutating

CNAME=notifier-spec-o
IMG=sentinel-notifier:latest
cleanup() { docker rm -f "$CNAME" >/dev/null 2>&1 || true; }
trap cleanup EXIT
cleanup

docker image inspect "$IMG" >/dev/null 2>&1 || skip "$IMG 이미지 없음 — 먼저 build 필요"

# 재시도/폴백 체인을 관측하려면 라이브 web-backend 에 연락처 ≥1 필요(읽기 전용 조회).
ncontacts=$(db_query "SELECT COUNT(*) FROM contacts WHERE deleted_at IS NULL;" 2>/dev/null \
  || db_query "SELECT COUNT(*) FROM contacts;" 2>/dev/null || echo 0)
[ "${ncontacts:-0}" -ge 1 ] || skip "연락처 0건 — 알림톡 시도가 안 돌아 재시도/폴백 관측 불가"

# 알림톡=연결거부(127.0.0.1:9, 즉시 실패=재시도 적격), RETRY_MAX=1. SMS 비활성 → 폴백은 시스템 알람.
docker run -d --name "$CNAME" --network "$NET" \
  -e KAKAO_ENABLED=true -e SMS_ENABLED=false \
  -e KAKAO_API_URL=http://127.0.0.1:9/nowhere -e KAKAO_API_KEY=x \
  -e KAKAO_SENDER_KEY=x -e KAKAO_TEMPLATE_CODE=T1 \
  -e CHANNEL_RETRY_MAX=1 -e CHANNEL_RETRY_BACKOFF_MS=200 \
  -e WEB_BACKEND_URL=http://web-backend:8080 -e RECORDING_URL=http://127.0.0.1:9 \
  "$IMG" >/dev/null || nok "격리 컨테이너 기동 실패"

up=0
for _ in $(seq 1 15); do
  c=$(bcode "http://$CNAME:8080/healthz" 2>/dev/null || true)
  [ "$c" = 200 ] && { up=1; break; }
  sleep 1
done
[ "$up" = 1 ] || { docker logs "$CNAME" 2>&1 | tail -5; nok "격리 notifier healthz 미기동"; }

TS=$(date -u +%Y-%m-%dT%H:%M:%SZ)
bcode -X POST "http://$CNAME:8080/api/notify" -H 'Content-Type: application/json' \
  -d "{\"siteId\":\"site1\",\"deviceId\":\"TEST-O1\",\"type\":\"gas_leak\",\"timestamp\":\"$TS\",\"test\":true}" >/dev/null
sleep 12
log=$(docker logs "$CNAME" 2>&1)

# 알림톡이 실제로 실패 경로를 탔는가(비공허 전제).
kakao_ran=$(printf '%s' "$log" | grep -ciE "KakaoTalk FAILED|\[kakao\]" || true)
# 같은 채널 재시도 증거(§13: 재시도 발생은 로그로 남는다).
retry=$(printf '%s' "$log" | grep -ciE "retry|retrying|재시도|attempt [2-9]" || true)
# 폴백 진행(SMS 비활성 → 시스템 알람 시도).
fell_back=$(printf '%s' "$log" | grep -ciE "System alarm|proceeding to system alarm" || true)
echo "kakao실패경로=$kakao_ran 재시도로그=$retry 폴백=$fell_back (RETRY_MAX=1)"
echo "SKIP-문서화: 타임아웃-비재시도 분기는 제어 가능한 느린 엔드포인트 부재로 본 런에서 미검증."

[ "$kakao_ran" -ge 1 ] || skip "알림톡 실패 경로 미실행(연락처 조회 실패 등) — 재시도 관측 전제 미충족"
[ "$retry" -ge 1 ] || nok "CHANNEL_RETRY_MAX=1 인데 같은 채널 재시도 로그가 없음 — 재시도 없이 즉시 폴백"
[ "$fell_back" -ge 1 ] || nok "재시도 후 폴백(시스템 알람) 진행 로그가 없음"
ok "연결거부 → 같은 채널 재시도 ${retry}건 → 폴백 관측"
