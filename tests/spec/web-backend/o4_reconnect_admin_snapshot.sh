#!/usr/bin/env bash
# O4 (⚠권장). 재접속 admin 스냅샷 재전달 — 어떤 감시 대상이 unhealthy 인 상태에서
#   admin WS 가 '새로' 접속하면 connected 직후 그 대상의 system_alarm(details.status=="unhealthy")
#   스냅샷을 수신한다(전이 순간 미접속 admin 의 유실 보정). user WS 는 스냅샷 미수신.
# spec: docs/spec/web-backend.md 단언 O4 / interface-web-api.md 계약 12·14
# SKIP: mutating — 센서 등록 + 임계 조정 + unhealthy 유도 후 재접속 관측. ALLOW_MUTATING=1 로만.
set -uo pipefail; . "$(dirname "$0")/../lib-web.sh"
T=$(get_token) || skip "(fixture 부재): admin 토큰 획득 실패"
require_mutating
SID="spectdd-o4-$(date +%s)-$$"; DID="SENSOR-1"; EID="$SID:$DID"
orig=$(bcurl -H "Authorization: Bearer $T" "$BACKEND/api/settings")
osa=$(echo "$orig" | jq -r '.[]|select(.key=="health.sensor_alive_threshold_sec").value')
oiv=$(echo "$orig" | jq -r '.[]|select(.key=="health.service_check_interval_sec").value')
restore() {
  [ -n "${osa:-}" ] && [ "$osa" != "null" ] && bcurl -X PUT -H "Authorization: Bearer $T" -H 'Content-Type: application/json' -d "{\"value\":\"$osa\"}" "$BACKEND/api/settings/health.sensor_alive_threshold_sec" >/dev/null 2>&1 || true
  [ -n "${oiv:-}" ] && [ "$oiv" != "null" ] && bcurl -X PUT -H "Authorization: Bearer $T" -H 'Content-Type: application/json' -d "{\"value\":\"$oiv\"}" "$BACKEND/api/settings/health.service_check_interval_sec" >/dev/null 2>&1 || true
  did=$(bcurl -H "Authorization: Bearer $T" "$BACKEND/api/devices/all" | jq -r '.[]|select(.siteId=="'"$SID"'" and .deviceId=="'"$DID"'").id' 2>/dev/null | head -1)
  [ -n "${did:-}" ] && [ "$did" != "null" ] && bcurl -X DELETE -H "Authorization: Bearer $T" "$BACKEND/api/devices/$did" >/dev/null 2>&1 || true
}
trap restore EXIT
bcurl -X PUT -H "Authorization: Bearer $T" -H 'Content-Type: application/json' -d '{"value":"5"}' "$BACKEND/api/settings/health.sensor_alive_threshold_sec" >/dev/null
bcurl -X PUT -H "Authorization: Bearer $T" -H 'Content-Type: application/json' -d '{"value":"5"}' "$BACKEND/api/settings/health.service_check_interval_sec" >/dev/null
bcurl -X POST -H 'Content-Type: application/json' -d "{\"siteId\":\"$SID\",\"deviceId\":\"$DID\"}" "$BACKEND/api/devices/seen" >/dev/null

# 대상이 unhealthy 로 전이할 때까지 대기 (임계 5s + 체크주기 5s 여유)
sleep 20
st=$(bcurl -H "Authorization: Bearer $T" "$BACKEND/api/health" | jq -r '.[]|select(.id=="'"$EID"'").status' 2>/dev/null)
echo "INFO: 대상 $EID 현재 상태 = ${st:-<none>} (unhealthy 전제)"

# 이제 admin WS 를 '새로' 접속 → connected 직후 스냅샷 기대
alog="$SPEC_TMP/o4_admin.log"; ulog="$SPEC_TMP/o4_user.log"
ws_observe "/ws?token=$T" 8 normal > "$alog" & apid=$!
upid=""
if [ -n "${USER_TOKEN:-}" ]; then ws_observe "/ws?token=$USER_TOKEN" 8 normal > "$ulog" & upid=$!; fi
wait $apid
[ -n "$upid" ] && wait "$upid" 2>/dev/null || true
snap=$(grep '^TEXT: ' "$alog" | sed 's/^TEXT: //' \
  | jq -rc 'select(.type=="system_alarm") | select(.payload.details.status=="unhealthy" and .payload.details.entityId=="'"$EID"'") | .payload.details' 2>/dev/null)
echo "admin 재접속 스냅샷 매치: ${snap:-<none>}"
[ -n "$snap" ] || nok "재접속 admin 이 unhealthy 스냅샷($EID) 미수신 (스냅샷 재전달 미구현/미발화)"
if [ -n "${USER_TOKEN:-}" ]; then
  ucount=$(grep '^TEXT: ' "$ulog" | sed 's/^TEXT: //' | jq -rc 'select(.type=="system_alarm")' 2>/dev/null | grep -c . || true)
  echo "user 스냅샷 수신 건수: $ucount (0이어야 함)"
  [ "$ucount" = "0" ] || nok "user WS 가 스냅샷 수신 (admin 전용 위반)"
fi
ok "재접속 admin unhealthy 스냅샷 수신"
