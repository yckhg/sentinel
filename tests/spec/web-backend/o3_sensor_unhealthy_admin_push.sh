#!/usr/bin/env bash
# O3. 센서 unhealthy 표면화 — 등록 센서 heartbeat 끊김 ≥ 임계 → admin WS 가
#     system_alarm(details.entityKind=="sensor"·entityId=="siteId:deviceId"·status=="unhealthy") 1건 수신.
#     user WS 미수신.
# spec: docs/spec/web-backend.md 단언 O3 / interface-web-api.md 계약 12·14
# SKIP: mutating — 센서 등록(devices.seen) + 건강 임계 조정 + WS 관측. ALLOW_MUTATING=1 로만.
#       user-부정 확인은 USER_TOKEN env 제공 시에만 수행.
set -uo pipefail; . "$(dirname "$0")/../lib-web.sh"
T=$(get_token) || skip "(fixture 부재): admin 토큰 획득 실패"
require_mutating

SID="spectdd-o3-$(date +%s)-$$"; DID="SENSOR-1"; EID="$SID:$DID"

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

# 센서 1회 등록(heartbeat) 후 더 이상 갱신하지 않아 last_seen 노후화 → unhealthy 전이
bcurl -X POST -H 'Content-Type: application/json' -d "{\"siteId\":\"$SID\",\"deviceId\":\"$DID\"}" "$BACKEND/api/devices/seen" >/dev/null

alog="$SPEC_TMP/o3_admin.log"; ulog="$SPEC_TMP/o3_user.log"
ws_observe "/ws?token=$T" 40 normal > "$alog" & apid=$!
upid=""
if [ -n "${USER_TOKEN:-}" ]; then ws_observe "/ws?token=$USER_TOKEN" 40 normal > "$ulog" & upid=$!; fi
sleep 2
wait $apid
[ -n "$upid" ] && wait "$upid" 2>/dev/null || true

amatch=$(grep '^TEXT: ' "$alog" | sed 's/^TEXT: //' \
  | jq -rc 'select(.type=="system_alarm") | select(.payload.details.entityKind=="sensor" and .payload.details.status=="unhealthy" and .payload.details.entityId=="'"$EID"'") | .payload.details' 2>/dev/null)
echo "admin system_alarm(sensor/unhealthy $EID) 매치: ${amatch:-<none>}"
[ -n "$amatch" ] || nok "admin WS 가 sensor/unhealthy($EID) system_alarm 미수신 (표면화 미구현/미발화)"
if [ -n "${USER_TOKEN:-}" ]; then
  ucount=$(grep '^TEXT: ' "$ulog" | sed 's/^TEXT: //' | jq -rc 'select(.type=="system_alarm")' 2>/dev/null | grep -c . || true)
  echo "user system_alarm 수신 건수: $ucount (0이어야 함)"
  [ "$ucount" = "0" ] || nok "user WS 가 system_alarm 수신 (admin 전용 위반)"
fi
ok "admin WS sensor/unhealthy system_alarm 수신 (n=$EID)"
