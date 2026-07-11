#!/usr/bin/env bash
# M. 대상 부재 시 시스템 알람 (silent fail 금지): 유효 위기 이벤트를 200 으로 수락했으나
#    비상연락처 조회가 실패하거나 결과가 0건인 경우에도, notifier 는 web-backend
#    POST /internal/alarms 로 `target_unavailable` 류 시스템 알람 전송을 **최소 1건 시도**하고
#    그 결과(2xx/비2xx/호출 실패)를 로그로 남긴다. 로그 한 줄로 조용히 소멸하면 안 된다.
# spec: docs/spec/notifier.md — 검증 단언 M (§출력 11)
# 판정: OK=exit0 (200 + target_unavailable 시도 결과 로그 ≥1), NOK=exit1, SKIPPED=exit2.
#
# 전제 조작: '연락처 0건 / 조회 실패' 를 프로덕션 스택에서 비파괴적으로 만들 수 없다. 따라서
# 이미 빌드된 sentinel-notifier 이미지를 재사용해 **격리 일회용 컨테이너**를 띄우되,
# WEB_BACKEND_URL 을 도달 불가로 지정해 fetchContacts 를 실패(§출력 11 의 '조회 실패' 분기)
# 시키고, 그때에도 target_unavailable 시스템 알람 시도가 로그로 남는지 관측한다.
# (0건 분기와 조회-실패 분기는 §11 에서 동치로 취급된다 — 둘 다 '대상 미확정'.)
set -uo pipefail
. "$(dirname "$0")/../lib-web.sh"

# 격리 컨테이너 기동은 상태를 만드는 행위이므로 mutating 게이트.
require_mutating

CNAME=notifier-spec-m
IMG=sentinel-notifier:latest
cleanup() { docker rm -f "$CNAME" >/dev/null 2>&1 || true; }
trap cleanup EXIT
cleanup

docker image inspect "$IMG" >/dev/null 2>&1 || skip "$IMG 이미지 없음 — 먼저 build 필요"

# WEB_BACKEND_URL/RECORDING_URL 모두 도달 불가 → 연락처 조회 실패(대상 미확정) + 라이브
# recording 무오염. (fixed 구현이 target_unavailable 알람도 이 도달불가 URL 로 시도하면
# 그 '호출 실패' 결과가 로그에 남는다 — §11 이 요구하는 '시도 + 결과 로그'.)
docker run -d --name "$CNAME" --network "$NET" \
  -e WEB_BACKEND_URL=http://127.0.0.1:9 \
  -e RECORDING_URL=http://127.0.0.1:9 \
  "$IMG" >/dev/null || nok "격리 컨테이너 기동 실패"

# healthz 대기 (최대 ~15s).
up=0
for _ in $(seq 1 15); do
  c=$(bcode "http://$CNAME:8080/healthz" 2>/dev/null || true)
  [ "$c" = 200 ] && { up=1; break; }
  sleep 1
done
[ "$up" = 1 ] || { docker logs "$CNAME" 2>&1 | tail -5; nok "격리 notifier healthz 미기동"; }

TS=$(date -u +%Y-%m-%dT%H:%M:%SZ)
code=$(bcode -X POST "http://$CNAME:8080/api/notify" -H 'Content-Type: application/json' \
  -d "{\"siteId\":\"site1\",\"deviceId\":\"TEST-M1\",\"type\":\"gas_leak\",\"timestamp\":\"$TS\"}")
sleep 8
log=$(docker logs "$CNAME" 2>&1)

# 시스템 알람 '대상 부재' 시도 결과 로그 (타입 문자열 target_unavailable 를 앵커로).
attempt=$(printf '%s' "$log" | grep -ciE "target_unavailable" || true)
echo "code=$code target_unavailable_시도로그=$attempt"

[ "$code" = 200 ] || nok "수락 실패 — 200 이 아님 (code=$code)"
[ "$attempt" -ge 1 ] || nok "대상 부재인데 target_unavailable 시스템 알람 시도 결과 로그가 없음 (silent fail)"
ok "200 수락 + target_unavailable 시스템 알람 시도 결과 로그 ${attempt}건"
