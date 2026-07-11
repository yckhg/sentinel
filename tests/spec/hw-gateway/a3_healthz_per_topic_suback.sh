#!/usr/bin/env bash
# A3 (healthz per-topic SUBACK 변별 — spec §헬스체크 계약, #51) — LOAD-BEARING SKIPPED.
#   계약: (a) candidate 구독 미성립만으로는 degraded 안 됨(참고 채널, 판정 제외),
#         (b) 경보성 3토픽(alert/heartbeat/resolved) 중 하나만 SUBACK 실패해도 degraded(503).
# spec: docs/spec/hw-gateway.md — §헬스체크(healthz) 계약 · ⚠#10/#51 · 단언 A/A2/O
#
# 왜 SKIPPED(부적절, requires-isolated-broker-ACL-harness):
#   per-topic SUBACK 실패를 주입하려면 브로커가 특정 토픽 SUBSCRIBE에 SUBACK 0x80(거부)을
#   돌려줘야 한다. 그러나 gateway는 고정 **와일드카드** 필터(safety/+/alert 등)로 구독하고,
#   mosquitto(2.x)는 와일드카드 SUBSCRIBE에 대해 **SUBACK을 항상 granted(≠0x80)로 응답**하고
#   ACL은 메시지 **전달 시점**에만 적용한다(구독 자체는 거부하지 않음). 따라서 ACL 격리 브로커로는
#   경보성 토픽의 0x80 SUBACK을 재현할 수 없다. 아래에서 이 사실을 실측으로 증명한다.
#   0x80을 강제하려면 특정 SUBSCRIBE에 0x80을 돌려주는 **커스텀 MQTT-프로토콜 목 브로커**가
#   필요한데, 이는 스펙이 "과도"로 규정한 범위다.
#
# 완화(vacuous 아님을 보증): per-topic 판정 로직 isHealthy()/recordGrant()/setConnected(grants clear)는
#   services/hw-gateway/main_test.go 유닛으로 이미 검증됨:
#     - TestIsHealthy: heartbeat=subackFailure(0x80) → degraded
#     - TestIsHealthyCandidateFailureIgnored: candidate=0x80 → 여전히 healthy
#     - TestHealthStateSetConnectedClearsGrants: 단절 시 grants clear → degraded
#   통합 레벨의 브로커-실측만 이 하네스로 불가하며, 로직 자체는 초록이 아니라 유닛으로 커버된다.
set -uo pipefail
cd "$(dirname "$0")"
. ./lib-gw-isolated.sh
. ../lib-web.sh

# --- Fix #2: load-bearing SKIP 사유를 mutating 게이트보다 먼저 무조건 표면화 --------
# read-only(기본) 실행에서도 이 스킵이 (초록 아닌 채로) 항상 보이도록, 아래 메타데이터를
# 실행모드와 무관하게 먼저 출력한다. 이전 버전은 require_mutating이 먼저 걸려 일반
# "(mutating — 승인대기)"로 exit → per-topic SUBACK 사유·중요도가 은폐되었다.
echo "LOAD-BEARING SKIP — A3 (healthz per-topic SUBACK 변별)"
echo "  단언ID     : A / A2 (spec §헬스체크 계약 · #51)"
echo "  종류       : 의도적(intentional) / 부적절(inappropriate) — requires-isolated-broker-ACL-harness"
echo "  사유       : mosquitto는 와일드카드 SUBSCRIBE에 SUBACK granted(≠0x80)를 반환하고 ACL은"
echo "               전달 시점에만 적용 → 경보성 토픽의 per-topic 0x80 SUBACK 실패를 주입 불가(통합 실측 불가)."
echo "  중요도     : load-bearing (경보 3토픽 중 하나라도 SUBACK 실패 시 503 계약)"
echo "  유닛 대체커버: services/hw-gateway/main_test.go"
echo "               TestIsHealthy / TestIsHealthyCandidateFailureIgnored / TestHealthStateSetConnectedClearsGrants"

SKIP_MSG="(부적절, requires-isolated-broker-ACL-harness): mosquitto는 와일드카드 SUBSCRIBE에 SUBACK granted를 반환(ACL은 전달 시점 적용)하여 경보성 토픽의 0x80 SUBACK 실패를 주입할 수 없음 — 통합 실측 불가. per-topic 변별 로직은 main_test.go 유닛(TestIsHealthy/CandidateFailureIgnored/SetConnectedClearsGrants)으로 커버됨. 0x80 강제는 커스텀 MQTT 목 브로커(과도) 필요."

# 실측 증거 + 전제 기계-가드는 컨테이너 다수 기동이 필요 → ALLOW_MUTATING 게이트.
# (require_mutating을 쓰지 않는다: 위 load-bearing 사유가 일반 mutating 메시지에 가려지지 않도록.)
if [ "${ALLOW_MUTATING:-0}" != 1 ]; then
  echo
  echo "실측 증거(브로커 SUBACK 관측)는 컨테이너 기동이 필요 → ALLOW_MUTATING=1 에서만 수행."
  echo "  (ALLOW_MUTATING=1이면 전제 'mosquitto가 와일드카드 SUBSCRIBE에 granted SUBACK 반환'을 기계적으로 확인한다.)"
  skip "$SKIP_MSG"
fi

# --- ALLOW_MUTATING=1: 전제를 실측·기계 검증한다 -----------------------------------
# 실측 증거: ACL로 alert 구독을 '거부'해도 mosquitto가 SUBACK을 granted로 준다.
if iso_preflight && iso_init; then
  trap iso_cleanup EXIT
  cat >"$ISO_DIR/acl-b" <<'EOF'
topic readwrite safety/+/heartbeat
topic readwrite safety/+/alert/resolved
topic readwrite safety/+/event/candidate
EOF
  BB="mosq-b-$ISO_TAG"; GB="gw-b-$ISO_TAG"
  iso_broker_acl "$BB" "$ISO_DIR/acl-b"
  iso_mock "mockw-b-$ISO_TAG" 200
  sleep 1
  iso_gw "$GB" "$BB" "http://mockw-b-$ISO_TAG:8080" "http://mockw-b-$ISO_TAG:8080"
  CB=""
  for i in $(seq 1 30); do CB=$(iso_code "$GB"); [ -n "$CB" ] && [ "$CB" != 000 ] && break; sleep 1; done
  sleep 3; CB=$(iso_code "$GB")
  # mosquitto가 와일드카드 alert 구독에 어떤 granted QoS를 주는지 실측(mosquitto_sub -d).
  #   "Subscribed (mid: N): <granted>"  — <granted> 0/1/2=수락, 128(0x80)=거부.
  SUBLINE=$(docker run --rm --network "$ISO_NET" "$MOSQ_IMG" \
    mosquitto_sub -h "$BB" -t 'safety/+/alert' -W 2 -d 2>&1 | grep -i 'Subscribed' | head -1)
  GRANTED=$(printf '%s' "$SUBLINE" | sed -n 's/.*:[[:space:]]*\([0-9]\{1,3\}\)[[:space:]]*$/\1/p')
  echo
  echo "실측: ACL이 alert 미허용인데도 → gateway /healthz=$CB"
  echo "실측: 브로커 SUBACK 라인 = ${SUBLINE:-<none>}"
  echo "실측: granted QoS = ${GRANTED:-<parse-fail>}  (128/0x80이면 구독 거부=전제 붕괴)"

  # --- Fix #3: 전제(SUBACK granted-always) 기계-가드 — 전제 붕괴가 침묵하지 않게 -----
  if [ "${GRANTED:-}" = 128 ]; then
    # 전제 붕괴: 브로커가 와일드카드 alert 구독을 0x80으로 거부.
    # → per-topic 0x80 주입이 가능해졌으므로 조용히 SKIP하지 말고 실통합 검증으로 승격한다.
    #   gateway도 동일 alert 필터로 0x80을 받으므로 spec(#51)상 degraded(503) 기대.
    echo "전제 붕괴 감지(granted==128): per-topic 0x80 주입이 가능해짐 → 실통합 검증으로 승격."
    if [ "$CB" = 503 ]; then
      ok "SUBACK 전제 붕괴로 per-topic 0x80 실측 가능 — gateway가 degraded(503)로 계약 준수(A2 실검증 성립)."
    else
      nok "전제 붕괴(alert SUBACK=0x80)인데 gateway /healthz=$CB(기대 503) — 제품 결함 또는 필터 불일치. 전제 붕괴를 침묵시키지 않음."
    fi
  elif [ -n "${GRANTED:-}" ]; then
    echo "전제 성립(granted=$GRANTED ≠128): mosquitto가 와일드카드 SUBSCRIBE를 granted로 수락 → per-topic 0x80 주입 불가."
    skip "$SKIP_MSG (실측: granted=$GRANTED, healthz=$CB)"
  else
    echo "granted 파싱 실패 — SUBACK 값 미관측. 전제를 기계 확인하지 못했으므로 문서화된 전제로 SKIP 유지."
    skip "$SKIP_MSG (granted 미관측)"
  fi
else
  echo "실측 생략: 격리 이미지/네트워크 준비 불가 — 아래 SKIP 사유는 mosquitto 계약상 동일하게 성립."
  skip "$SKIP_MSG (격리 준비 불가)"
fi
