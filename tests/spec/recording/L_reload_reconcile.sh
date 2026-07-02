#!/usr/bin/env bash
# 단언 L. reload reconcile — 카메라 비활성화 + POST /api/cameras/reload 후
#   /api/status에서 k 소멸·세그먼트 생성 중단, 재활성화 + reload 후 녹화 재개.
# SKIP: 전 과정이 MUTATING —
#   - POST /api/cameras/reload 는 프로덕션 recorder를 중지/재기동시킴
#   - 특히 ⚠️ 8: web-backend 조회가 일시 실패하면 reload 한 번으로 "모든" recorder가
#     중지됨 (빈 목록 reconcile) — 가동 중 증거 녹화 전체가 끊길 실위험
#   - 카메라 비활성화/활성화도 web-backend 상태 변경
# 설계자 승인(ALLOW_MUTATING=1) 대기.
. "$(dirname "$0")/common.sh"

if [ "${ALLOW_MUTATING:-0}" != "1" ]; then
  skip_mutating L "reload는 recorder 중지 유발(⚠️8: web-backend 조회 실패 시 전체 녹화 중단 위험) + 카메라 설정 변경 필요"
fi

# ---- MUTATING PART (승인 시에만) ----
k=$(active_key)
echo "  [!!] 1) web-backend에서 $k 비활성화 후:"
body=$(mktemp)
http_get "$REC/api/cameras/reload" "$body"   # 주의: 실제로는 POST 필요 — busybox wget: wget --post-data=''
docker exec "$REC_CONTAINER" sh -c "wget -qO- --post-data='' $REC/api/cameras/reload"
sleep 5
rexec "wget -qO- $REC/api/status" | grep -q "\"$k\"" && nok "reload 후에도 $k 존재" || ok "reload 후 $k 소멸"
echo "  [!!] 2) 재활성화 + reload 후 status=recording 재확인 필요"
rm -f "$body"
verdict L
