#!/usr/bin/env bash
# SKIP: mutating — POST /api/cameras/reload 는 push 프로세스 집합을 재조정(reconcile)함.
#   프로덕션 어댑터의 카메라 구성을 web-backend 목록으로 교체하는 상태 변경 (설계자 승인 대기).
#   또한 web-backend 목록을 특정 픽스처(rtsp 1 + youtube 1 + disabled 1)로 만들어야 함.
# cctv-adapter §단언 F (reload 성공 경로): 픽스처 목록에서 rtsp+enabled+비어있지 않은
#   sourceUrl/streamKey 만 채택 → 200 {"status":"reloaded","cameras":1}, status에 cam-a만 존재.
set -u

if [ "${SPEC_TDD_ALLOW_MUTATING:-0}" != "1" ]; then
  echo "SKIPPED: POST reload mutates production push-process set; needs web-backend fixture."
  exit 2
fi

OUT=$(docker exec sentinel-cctv-adapter wget -qO- --post-data='' http://localhost:8080/api/cameras/reload)
echo "reload: $OUT"
printf '%s' "$OUT" | jq -e '.status=="reloaded" and .cameras==1' >/dev/null || { echo "NOK: unexpected reload response"; exit 1; }
S=$(docker exec sentinel-cctv-adapter wget -qO- http://localhost:8080/api/cameras/status)
echo "status: $S"
printf '%s' "$S" | jq -e 'length==1 and .[0].cameraId=="cam-a"' >/dev/null \
  && { echo "OK"; exit 0; }
echo "NOK: status list not [cam-a]"
exit 1
