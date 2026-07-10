#!/usr/bin/env bash
# SKIP: mutating — B-frame 포함 테스트 push는 프로덕션 streaming에 연결을 생성함 (설계자 승인 대기)
# A1-3 (B-frame 거부): -bf 2 push가 약 5초 내 연결 종료되면 OK (금지 조항 실효성 확인).
#   정상 지속되면 NOK → nginx-rtmp 버전 변경 → 스펙 재검토 트리거.
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
if [ "$RC" -ne 0 ] && [ "$ELAPSED" -le 15 ]; then
  echo "OK: B-frame push terminated early (~${ELAPSED}s) — 금지 조항 유효"
  exit 0
fi
echo "NOK: B-frame push sustained (rc=$RC, ${ELAPSED}s) — nginx-rtmp 동작 변경, 스펙 재검토 필요"
exit 1
