#!/usr/bin/env bash
# T. 기동 불변식 (TTL > HEARTBEAT): EQUIPMENT_EVICT_TTL_SEC ≤ HEARTBEAT_TIMEOUT_SEC 위반으로 기동
#    → 잘못된 TTL 미채택: 경고 로그 + EQUIPMENT_EVICT_TTL_SEC 기본값 86400 강제 대체.
#    (⚠ 대안: 기동 거부(fail-fast) 채택 시 프로세스가 비정상 종료 — 이 경우도 OK로 인정.)
# spec: docs/spec/hw-gateway.md — 검증 단언 T · §장비 스토어 보존 상한·eviction(기동 불변식)
#
# 격리: 전용 throwaway gateway. 브로커 불필요(기동 불변식은 연결과 무관하게 부팅 시 평가) —
#       도달 불가 브로커 URL로 띄워 라이브 브로커/clientID 충돌을 원천 차단한다. --rm 미사용:
#       fail-fast로 종료해도 로그·exit code 사후 조회가 가능해야 한다.
set -uo pipefail
. "$(dirname "$0")/../lib-web.sh"

require_mutating

IMG=${HWGW_IMG:-sentinel-hw-gateway:latest}
docker image inspect "$IMG" >/dev/null 2>&1 \
  || skip "hw-gateway 이미지($IMG) 부재 — feat/hw-gateway 빌드 후 검증 (verifier 이미지 보유 시 green)"

GW="sentinel-ttlinv-gw-$$"
cleanup() { docker rm -f "$GW" >/dev/null 2>&1 || true; }
trap cleanup EXIT

# 위반 설정: TTL(10) ≤ HEARTBEAT(30)
docker run -d --network "$NET" --name "$GW" \
  -e HEARTBEAT_TIMEOUT_SEC=30 \
  -e EQUIPMENT_EVICT_TTL_SEC=10 \
  -e MQTT_BROKER_URL="tcp://127.0.0.1:9" \
  -e WEB_BACKEND_URL="http://127.0.0.1:9" \
  -e NOTIFIER_URL="http://127.0.0.1:9" \
  "$IMG" >/dev/null || nok "throwaway gateway 기동 실패(런타임 오류)"
sleep 4

logs=$(docker logs "$GW" 2>&1)
echo "--- gateway startup logs ---"
printf '%s\n' "$logs" | head -n 30

# 컨테이너가 종료됐다면 fail-fast(기동 거부) 대안 채택 여부 확인
if ! docker ps --format '{{.Names}}' | grep -qx "$GW"; then
  ec=$(docker inspect -f '{{.State.ExitCode}}' "$GW" 2>/dev/null || echo "?")
  if [ "$ec" != "0" ] && [ "$ec" != "?" ]; then
    ok "TTL 불변식 위반 → 기동 거부(fail-fast, exit=$ec) — ⚠대안 정책 채택으로 인정"
  fi
  nok "컨테이너 종료됐으나 exit=0/불명($ec) — 불변식 처리 불명확"
fi

# 기본 정책: 경고 로그 + 86400 강제 대체
if printf '%s' "$logs" | grep -qi -e 'ttl' -e 'evict' && printf '%s' "$logs" | grep -q '86400'; then
  ok "TTL 불변식 위반(TTL≤HEARTBEAT) → 경고 로그 + 기본값 86400 강제 대체 확인"
fi
nok "기동 불변식 미적용: 경고/86400 강제 로그 미관측 — 잘못된 TTL(10) 조용히 채택 위험(RED)"
