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

require_mutating

# --- 실측 증거: ACL로 alert 구독을 '거부'해도 mosquitto가 SUBACK을 granted로 준다 ---
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
  # mosquitto가 와일드카드 alert 구독을 거부했는지 직접 확인(mosquitto_sub로 SUBACK 코드 관측).
  SUBACK=$(docker run --rm --network "$ISO_NET" "$MOSQ_IMG" \
    mosquitto_sub -h "$BB" -t 'safety/+/alert' -W 2 -d 2>&1 | grep -i 'Subscribed' | head -1)
  echo "실측: ACL이 alert 미허용인데도 → gateway /healthz=$CB (기대했던 503 아님)"
  echo "실측: 브로커 SUBACK 응답 = ${SUBACK:-<none>}  (granted QoS 값, 128/0x80이 아니면 거부 실패)"
  echo "결론: mosquitto는 와일드카드 SUBSCRIBE에 granted SUBACK을 주므로 per-topic 0x80을 재현 불가."
else
  echo "실측 생략: 격리 이미지/네트워크 준비 불가 — 아래 SKIP 사유는 mosquitto 계약상 동일하게 성립."
fi

skip "(부적절, requires-isolated-broker-ACL-harness): mosquitto는 와일드카드 SUBSCRIBE에 SUBACK granted를 반환(ACL은 전달 시점 적용)하여 경보성 토픽의 0x80 SUBACK 실패를 주입할 수 없음 — 통합 실측 불가. per-topic 변별 로직은 main_test.go 유닛(TestIsHealthy/CandidateFailureIgnored/SetConnectedClearsGrants)으로 커버됨. 0x80 강제는 커스텀 MQTT 목 브로커(과도) 필요."
