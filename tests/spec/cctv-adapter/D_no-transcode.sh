#!/usr/bin/env bash
# SKIP: 관측 불가 — 단언 C 상태(RTSP 카메라 push 중)가 전제이나 현재 카메라 0대 구성.
# cctv-adapter §단언 D (무 트랜스코딩): HLS 출력의 codec_name/width/height 가
#   입력 RTSP 스트림과 동일하면 OK. (ffprobe 양측 read-only pull 비교)
set -u

if [ "${SPEC_TDD_ALLOW_MUTATING:-0}" != "1" ]; then
  echo "SKIPPED: precondition absent — no RTSP camera configured (assertion C state required)."
  exit 2
fi

KEY="${STREAM_KEY:?set STREAM_KEY}"
RTSP_URL="${RTSP_URL:?set RTSP_URL of the camera}"
PROBE="-v error -select_streams v:0 -show_entries stream=codec_name,width,height -of csv=p=0"
IN=$(timeout 60 docker exec sentinel-cctv-adapter ffprobe -rtsp_transport tcp $PROBE "$RTSP_URL" 2>&1 | tail -1)
OUT=$(timeout 60 docker exec sentinel-cctv-adapter ffprobe $PROBE "http://streaming:8080/live/${KEY}/index.m3u8" 2>&1 | tail -1)
echo "RTSP in : $IN"
echo "HLS out : $OUT"
[ -n "$IN" ] && [ "$IN" = "$OUT" ] && { echo "OK: codec/resolution pass-through"; exit 0; }
echo "NOK: input/output mismatch (transcoding suspected)"
exit 1
