#!/usr/bin/env bash
# SKIP: mutating — reload(reconcile) 3종(동일/URL변경/제거) 시나리오는 web-backend 카메라
#   데이터 변경 + POST reload 반복이 필요. push 프로세스 재시작·중단을 유발 (설계자 승인 대기).
# cctv-adapter §단언 H (reconcile 무중단):
#   (1) 동일 streamKey+sourceUrl reload → connectedAt 불변(무중단)
#   (2) sourceUrl 변경 reload → connectedAt 갱신 + 새 URL push
#   (3) 목록 제거 reload → status에서 소멸 + RTMP push 중단
set -u

if [ "${SPEC_TDD_ALLOW_MUTATING:-0}" != "1" ]; then
  echo "SKIPPED: reconcile scenarios mutate camera set and push processes."
  exit 2
fi

echo "Manual fixture protocol (requires web-backend camera CRUD):"
echo " 1) capture connectedAt of cam-a; POST reload with identical list; assert connectedAt unchanged"
echo " 2) change sourceUrl of cam-a in web-backend; POST reload; assert connectedAt advanced"
echo " 3) delete cam-a; POST reload; assert absent from status and /api/streams active=false within window"
exit 1
