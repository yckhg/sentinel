#!/usr/bin/env bash
# 단언 L. reload reconcile — 카메라 비활성화 + POST /api/cameras/reload 후
#   /api/status에서 k 소멸(녹화 중단), 재활성화 + reload 후 녹화 재개.
# SKIP: 전 과정이 MUTATING —
#   - POST /api/cameras/reload 는 프로덕션 recorder를 중지/재기동시킴
#   - 특히 ⚠️ 8: web-backend 조회가 일시 실패하면 reload 한 번으로 "모든" recorder가
#     중지됨 (빈 목록 reconcile) — 가동 중 증거 녹화 전체가 끊길 실위험
#   - 카메라 enabled 토글도 web-backend 상태 변경
# 설계자 승인(ALLOW_MUTATING=1) 대기.
#
# NOTE(harness fix): 이전 버전은 카메라를 web-backend에서 비활성화하지 않고 reload만 해서,
#   reconcile 이 정상적으로 k 를 유지하는 것을 NOK 로 오판했다(false-NOK). reconcile 계약을
#   실제로 검증하려면 대상 카메라를 disable → reload → k 소멸 확인 → re-enable → reload → 재개
#   확인까지 수행해야 한다. web-backend 토큰/카메라 API 는 lib-web.sh 헬퍼를 재사용한다.
. "$(dirname "$0")/../lib-web.sh"   # get_token, bcurl, BACKEND
. "$(dirname "$0")/common.sh"       # rexec, active_key, ok/nok/verdict (뒤에 소스 → recording 판정 헬퍼 우선)

if [ "${ALLOW_MUTATING:-0}" != "1" ]; then
  skip_mutating L "reload는 recorder 중지 유발(⚠️8) + web-backend 카메라 enabled 토글 필요"
fi

# ---- MUTATING PART (승인 시에만) ----
k=$(active_key)
[ -z "$k" ] && { echo "VERDICT L: SKIPPED (녹화 중인 활성 스트림 없음 — no-data)"; exit 0; }
echo "  [..]  대상 활성 스트림 streamKey=$k"

# streamKey==k 인 카메라 id 조회 (internal, 무인증)
cam_id=$(bcurl "$BACKEND/internal/cameras" | jq -r ".[] | select(.streamKey==\"$k\") | .id" 2>/dev/null | head -1)
[ -z "$cam_id" ] && { echo "VERDICT L: SKIPPED (streamKey=$k 에 매칭되는 카메라 없음 — no-data)"; exit 0; }
echo "  [..]  web-backend 카메라 id=$cam_id"

T=$(get_token) || { echo "VERDICT L: SKIPPED (admin 토큰 획득 실패 — fixture 부재)"; exit 0; }

# 실패해도 카메라를 반드시 재활성화 (프로덕션 상태 복구)
restore_camera() {
  bcurl -X PUT -H "Authorization: Bearer $T" -H 'Content-Type: application/json' \
    -d '{"enabled":true}' "$BACKEND/api/cameras/$cam_id" >/dev/null 2>&1 || true
  rexec "wget -qO- --post-data='' $REC/api/cameras/reload" >/dev/null 2>&1 || true
}
trap restore_camera EXIT

# 1) 카메라 비활성화 + reload → k 소멸(녹화 중단) 확인
echo "  [!!] 1) web-backend 에서 카메라 $cam_id 비활성화 후 reload:"
bcurl -X PUT -H "Authorization: Bearer $T" -H 'Content-Type: application/json' \
  -d '{"enabled":false}' "$BACKEND/api/cameras/$cam_id" >/dev/null
rexec "wget -qO- --post-data='' $REC/api/cameras/reload" >/dev/null
sleep 5
if rexec "wget -qO- $REC/api/status" | grep -q "\"$k\""; then
  nok "disable+reload 후에도 $k 가 status 에 잔존 (reconcile 미반영)"
else
  ok "disable+reload 후 $k 소멸 (녹화 중단)"
fi

# 2) 재활성화 + reload → 녹화 재개 확인
echo "  [!!] 2) 카메라 $cam_id 재활성화 후 reload → 녹화 재개 확인:"
bcurl -X PUT -H "Authorization: Bearer $T" -H 'Content-Type: application/json' \
  -d '{"enabled":true}' "$BACKEND/api/cameras/$cam_id" >/dev/null
rexec "wget -qO- --post-data='' $REC/api/cameras/reload" >/dev/null
sleep 8
if rexec "wget -qO- $REC/api/status" | grep -q "\"$k\""; then
  ok "re-enable+reload 후 $k 녹화 재개"
else
  nok "re-enable+reload 후에도 $k 녹화 미재개"
fi

verdict L
