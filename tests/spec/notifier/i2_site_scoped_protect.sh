#!/usr/bin/env bash
# I(강화). 녹화 보호 site 한정: 서로 다른 두 site A/B 의 활성 카메라가 존재할 때 site A 이벤트를
#    주입하면, 보호 대상 streamKey 는 site A 카메라만 포함하고 site B 카메라는 포함하지 않는다
#    (다중 site 오염 방지). 관측: [archive] 보호 수락 로그의 '(N cameras)' 가 site A 활성 카메라
#    수와 같아야 하며 전체 활성 카메라 수(A+B)와 같아선 안 된다.
# spec: docs/spec/notifier.md — 검증 단언 I 의 site-한정 절 (§출력 6)
#    · 기존 i_archive_protect.sh(보호 트리거 자체)는 건드리지 않는다 — 이 스크립트는 site-제외 케이스.
#    · 부수 절 'recording 이 내려가도 알림 결과는 불변'은 단언 C/J 가 커버(여기선 count 에 집중).
#    · 이 site 한정은 web-backend /internal/cameras 가 카메라별 siteId 를 노출한다는 전제에 의존
#      (interface-web-api 소관) — 미노출이면 fixed 구현도 필터 불가하므로 현재 이중으로 RED.
# 판정: OK=exit0 (보호 count == siteA 활성 수), NOK=exit1 (전체 활성 수 = site B 오염), SKIPPED=exit2.
set -uo pipefail
. "$(dirname "$0")/../lib-web.sh"
NOTIFIER=${NOTIFIER:-http://notifier:8080}

require_mutating

total_enabled=$(db_query "SELECT COUNT(*) FROM cameras WHERE enabled=1;" 2>/dev/null || echo 0)
[ "${total_enabled:-0}" -ge 1 ] || skip "활성 카메라 0대 — 보호 요청 자체가 생략됨"

# site 별 활성 카메라 수 (비어있지 않은 site_id 만). site A = 활성 최다 site.
rows=$(db_query "SELECT site_id, COUNT(*) FROM cameras WHERE enabled=1 AND site_id<>'' GROUP BY site_id ORDER BY 2 DESC;" 2>/dev/null || true)
SITEA=$(printf '%s\n' "$rows" | head -1 | cut -d'|' -f1)
A_ENABLED=$(printf '%s\n' "$rows" | head -1 | cut -d'|' -f2)
echo "sites(enabled)=[$(printf '%s' "$rows" | tr '\n' ' ')] total_enabled=$total_enabled siteA=$SITEA a_enabled=$A_ENABLED"

[ -n "$SITEA" ] && [ "${A_ENABLED:-0}" -ge 1 ] || skip "비어있지 않은 site_id 를 가진 활성 카메라 없음 — site 구분 불가"
# site 제외를 관측하려면 site A 밖에도 활성 카메라가 있어야 한다(total > A).
[ "$total_enabled" -gt "$A_ENABLED" ] || skip "site A 밖 활성 카메라 없음(total=$total_enabled==A=$A_ENABLED) — 제외 대상 부재로 관측 불가"

SINCE=$(date -u +%Y-%m-%dT%H:%M:%SZ)
TS=$(date -u +%Y-%m-%dT%H:%M:%SZ)
bcode -X POST "$NOTIFIER/api/notify" -H 'Content-Type: application/json' \
  -d "{\"siteId\":\"$SITEA\",\"deviceId\":\"TEST-I2\",\"type\":\"gas_leak\",\"timestamp\":\"$TS\",\"test\":true}" >/dev/null
sleep 10

# incident ID 에 siteId 가 박히므로 site A 이벤트의 보호 로그를 정확히 특정한다.
line=$(docker logs sentinel-notifier --since "$SINCE" 2>&1 \
  | grep -E "\[archive\] Protect request accepted for incident incident_${SITEA}_" | head -1)
echo "protect_line=${line:-<none>}"
[ -n "$line" ] || skip "site A 보호 수락 로그 없음 (recording 다운/보호 생략) — count 관측 불가"

count=$(printf '%s' "$line" | grep -oE '\(([0-9]+) cameras\)' | grep -oE '[0-9]+' | head -1)
echo "보호대상수=$count siteA활성=$A_ENABLED 전체활성=$total_enabled"

[ "$count" = "$A_ENABLED" ] || nok "보호 대상 카메라 수=$count — site A 활성 수($A_ENABLED)와 불일치 (전체=$total_enabled: 타 site 카메라 오염)"
ok "보호 대상이 site A 활성 카메라 ${count}대로 한정 — 타 site 제외 확인"
