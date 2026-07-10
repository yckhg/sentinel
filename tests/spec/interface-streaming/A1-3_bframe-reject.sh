#!/usr/bin/env bash
# SKIP: mutating — B-frame 포함 테스트 push는 프로덕션 streaming에 연결을 생성함 (설계자 승인 대기)
# A1-3 (remux-only 코덱 무검사): 허브(nginx-rtmp v1.2.2)는 remux 전용이라 B-frame을 거부하지
#   않고 통과시킨다. -bf 2 push가 지속되면(rc=0, ~sustained) OK — 허브 코덱 무검사 계약 확인.
#   조기 종료(거부)면 NOK → nginx-rtmp 동작 변경 → 스펙 재검토 트리거.
#   (B-frame 금지는 허브가 아니라 push 측 어댑터 계약이다.)
# NOTE(계약 정정, 코드-실측 타이브레이크, domain3 감사): 이전 단언 "~5초 내 연결 종료(거부)되면
#   OK"는 허위 전제였다. `-re -pix_fmt yuv420p -bf 2` push 25초 완주·active m3u8 실측.
#   허브측 거부 단언을 "허브가 통과시킴(remux-only)"으로 재정의.
set -u

if [ "${SPEC_TDD_ALLOW_MUTATING:-0}" != "1" ]; then
  echo "SKIPPED: mutating assertion (B-frame RTMP push to production streaming). Set SPEC_TDD_ALLOW_MUTATING=1 to run."
  exit 2
fi

KEY="${STREAM_KEY:-spec-test-a1-bf}"
START=$(date +%s)
docker run --rm --network sentinel_sentinel-net --entrypoint ffmpeg linuxserver/ffmpeg \
  -re -f lavfi -i "testsrc=size=640x360:rate=15" -f lavfi -i sine \
  -t 60 -c:v libx264 -pix_fmt yuv420p -profile:v main -bf 2 -g 30 -c:a aac \
  -f flv "rtmp://streaming:1935/live/${KEY}"
RC=$?
ELAPSED=$(( $(date +%s) - START ))
echo "ffmpeg rc=$RC elapsed=${ELAPSED}s"
if [ "$RC" -eq 0 ] && [ "$ELAPSED" -ge 55 ]; then
  echo "OK: B-frame push accepted/sustained (~${ELAPSED}s) — 허브 remux-only 코덱 무검사 확인"
  exit 0
fi
echo "NOK: B-frame push terminated early (rc=$RC, ${ELAPSED}s) — nginx-rtmp 거부 동작(버전 변경?), 스펙 재검토 필요"
exit 1
