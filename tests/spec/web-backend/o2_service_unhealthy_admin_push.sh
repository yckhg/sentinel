#!/usr/bin/env bash
# O2. unhealthy 전이 admin push — 동료 서비스 다운+임계 경과 → 접속 중 admin WS 가
#     type=system_alarm(details.entityKind=="service"·해당 entityId·status=="unhealthy") 1건 수신.
#     user(비-admin) WS 는 미수신. (계약 14 health-출처 하위 스키마)
# spec: docs/spec/web-backend.md 단언 O2 / interface-web-api.md 계약 12·14
# SKIP: mutating(infra) — 동료 서비스 중지 + 건강 임계 조정 필요. ALLOW_MUTATING=1 로만.
#       user-부정 확인은 USER_TOKEN env(비-admin JWT) 제공 시에만 수행.
set -uo pipefail; . "$(dirname "$0")/../lib-web.sh"

# read-only 보조: admin WS 봉투 형태만 확인 (system_alarm 전이는 서비스 다운 없이는 관측 불가).
T=$(get_token) || skip "(fixture 부재): admin 토큰 획득 실패"
pre=$(ws_observe "/ws?token=$T" 4 normal)
echo "$pre" | grep -q '^HTTP: HTTP/1.1 101' || nok "admin WS 연결 미성립"
echo "INFO: admin WS 연결 성립 — 전이 push 는 서비스 다운 트리거 없이는 관측 불가"

require_mutating

DOWN=sentinel-notifier          # 중지 부작용이 가장 작은 동료(초대 이메일 전용)
SVC=notifier                    # health service entityId (서비스명)

# 건강 임계를 낮춰 전이를 빠르게 유도 (단건 PUT — 계약 11, 종료 시 원복)
orig=$(bcurl -H "Authorization: Bearer $T" "$BACKEND/api/settings")
oiv=$(echo "$orig" | jq -r '.[]|select(.key=="health.service_check_interval_sec").value')
odn=$(echo "$orig" | jq -r '.[]|select(.key=="health.service_down_threshold_sec").value')
restore() {
  [ -n "${oiv:-}" ] && [ "$oiv" != "null" ] && bcurl -X PUT -H "Authorization: Bearer $T" -H 'Content-Type: application/json' -d "{\"value\":\"$oiv\"}" "$BACKEND/api/settings/health.service_check_interval_sec" >/dev/null 2>&1 || true
  [ -n "${odn:-}" ] && [ "$odn" != "null" ] && bcurl -X PUT -H "Authorization: Bearer $T" -H 'Content-Type: application/json' -d "{\"value\":\"$odn\"}" "$BACKEND/api/settings/health.service_down_threshold_sec" >/dev/null 2>&1 || true
  docker start "$DOWN" >/dev/null 2>&1 || true
}
trap restore EXIT
bcurl -X PUT -H "Authorization: Bearer $T" -H 'Content-Type: application/json' -d '{"value":"5"}' "$BACKEND/api/settings/health.service_check_interval_sec" >/dev/null
bcurl -X PUT -H "Authorization: Bearer $T" -H 'Content-Type: application/json' -d '{"value":"5"}' "$BACKEND/api/settings/health.service_down_threshold_sec" >/dev/null

alog="$SPEC_TMP/o2_admin.log"; ulog="$SPEC_TMP/o2_user.log"
ws_observe "/ws?token=$T" 40 normal > "$alog" & apid=$!
upid=""
if [ -n "${USER_TOKEN:-}" ]; then ws_observe "/ws?token=$USER_TOKEN" 40 normal > "$ulog" & upid=$!; fi
sleep 2
docker stop "$DOWN" >/dev/null
wait $apid
[ -n "$upid" ] && wait "$upid" 2>/dev/null || true

amatch=$(grep '^TEXT: ' "$alog" | sed 's/^TEXT: //' \
  | jq -rc 'select(.type=="system_alarm") | select(.payload.details.entityKind=="service" and .payload.details.status=="unhealthy" and (.payload.details.entityId|test("'"$SVC"'"))) | .payload.details' 2>/dev/null)
echo "admin system_alarm(service/unhealthy) 매치: ${amatch:-<none>}"
[ -n "$amatch" ] || nok "admin WS 가 service/unhealthy system_alarm 미수신 (전이 push 미구현/미발화)"

if [ -n "${USER_TOKEN:-}" ]; then
  ucount=$(grep '^TEXT: ' "$ulog" | sed 's/^TEXT: //' | jq -rc 'select(.type=="system_alarm")' 2>/dev/null | grep -c . || true)
  echo "user system_alarm 수신 건수: $ucount (0이어야 함)"
  [ "$ucount" = "0" ] || nok "user WS 가 system_alarm 수신 (admin 전용 위반)"
else
  echo "INFO: USER_TOKEN 미제공 — user-부정 단언은 확인 생략(관측 불가)"
fi
ok "admin WS service/unhealthy system_alarm 수신 (user 부정은 USER_TOKEN 제공 시 확인)"
