#!/usr/bin/env bash
# V(#95). 설정 벌크 원자성·검증 — PUT /api/settings 벌크에 (a) 미지 key 또는 (b) 무효 값이
#   하나라도 있으면 400(리소스-부재 404 아님) + 부분 저장 0; 전부 유효면 200 + 전 항목 반영.
# spec: docs/spec/web-backend.md 단언 V / interface-web-api.md 계약 11 (known-key 타입/범위/교차제약 표)
# NOTE: 파일명 w_* — 기존 v_null_alertid_coexistence.sh(실제로 단언 T)를 덮어쓰지 않기 위함. 그 파일은 미변경.
# SKIP: mutating — 운영 health.* 설정 벌크 저장(종료 시 원복). ALLOW_MUTATING=1 로만.
set -uo pipefail; . "$(dirname "$0")/../lib-web.sh"
require_mutating
T=$(get_token) || exit 1
get_val() { bcurl -H "Authorization: Bearer $T" "$BACKEND/api/settings" | jq -r '.[]|select(.key=="'"$1"'").value'; }
K1=health.service_check_interval_sec
K2=health.sensor_alive_threshold_sec
K3=health.service_down_threshold_sec
o1=$(get_val "$K1"); o2=$(get_val "$K2"); o3=$(get_val "$K3")
echo "baseline: $K1=$o1 $K2=$o2 $K3=$o3"
restore() {
  [ -n "$o1" ] && [ "$o1" != "null" ] && bcurl -X PUT -H "Authorization: Bearer $T" -H 'Content-Type: application/json' -d "{\"value\":\"$o1\"}" "$BACKEND/api/settings/$K1" >/dev/null 2>&1 || true
  [ -n "$o2" ] && [ "$o2" != "null" ] && bcurl -X PUT -H "Authorization: Bearer $T" -H 'Content-Type: application/json' -d "{\"value\":\"$o2\"}" "$BACKEND/api/settings/$K2" >/dev/null 2>&1 || true
  [ -n "$o3" ] && [ "$o3" != "null" ] && bcurl -X PUT -H "Authorization: Bearer $T" -H 'Content-Type: application/json' -d "{\"value\":\"$o3\"}" "$BACKEND/api/settings/$K3" >/dev/null 2>&1 || true
}
trap restore EXIT

# (a) 유효 key 2개 + 미지 key 1개 → 400, 부분 저장 0
ca=$(bcode -X PUT -H "Authorization: Bearer $T" -H 'Content-Type: application/json' \
  -d "{\"$K1\":\"40\",\"$K2\":\"40\",\"spectdd_unknown_key\":\"x\"}" "$BACKEND/api/settings")
a1=$(get_val "$K1"); a2=$(get_val "$K2")
echo "(a) unknown-key bulk code=$ca  after: $K1=$a1 $K2=$a2 (원상 유지 기대)"
[ "$ca" = "400" ] || nok "(a) 미지 key 벌크가 400 아님 ($ca — 404면 벌크 계약 미준수)"
{ [ "$a1" = "$o1" ] && [ "$a2" = "$o2" ]; } || nok "(a) 부분 저장 발생 ($K1 $o1->$a1, $K2 $o2->$a2)"

# (b) 유효 key 1개 + 무효 값 1개(하한 미만 <5) → 400, 부분 저장 0
cb=$(bcode -X PUT -H "Authorization: Bearer $T" -H 'Content-Type: application/json' \
  -d "{\"$K2\":\"40\",\"$K1\":\"2\"}" "$BACKEND/api/settings")
b1=$(get_val "$K1"); b2=$(get_val "$K2")
echo "(b) invalid-value bulk code=$cb  after: $K1=$b1 $K2=$b2 (원상 유지 기대)"
[ "$cb" = "400" ] || nok "(b) 무효 값 벌크가 400 아님 ($cb)"
{ [ "$b1" = "$o1" ] && [ "$b2" = "$o2" ]; } || nok "(b) 부분 저장 발생 ($K1 $o1->$b1, $K2 $o2->$b2)"

# (c) 전부 유효 → 200, 전 항목 반영 (교차제약: down(120) >= interval(20))
cc=$(bcode -X PUT -H "Authorization: Bearer $T" -H 'Content-Type: application/json' \
  -d "{\"$K1\":\"20\",\"$K2\":\"45\",\"$K3\":\"120\"}" "$BACKEND/api/settings")
v1=$(get_val "$K1"); v2=$(get_val "$K2"); v3=$(get_val "$K3")
echo "(c) all-valid bulk code=$cc  after: $K1=$v1 $K2=$v2 $K3=$v3"
[ "$cc" = "200" ] || nok "(c) 전부 유효 벌크가 200 아님 ($cc)"
{ [ "$v1" = "20" ] && [ "$v2" = "45" ] && [ "$v3" = "120" ]; } || nok "(c) 유효 벌크 전항목 미반영"
ok "벌크 원자성·검증: (a)400+부분0 (b)400+부분0 (c)200+전반영"
