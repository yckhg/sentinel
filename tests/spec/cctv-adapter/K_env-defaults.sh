#!/usr/bin/env bash
# SKIP: mutating — 환경변수 미설정/오설정 기동은 별도 컨테이너 기동이 필요
#   (동일 네트워크에서 임시 컨테이너 기동 = 프로덕션 네트워크에 새 서비스 투입, 설계자 승인 대기).
# cctv-adapter §단언 K (환경변수 기본값): 4개 변수 미설정 기동 시 기본값 동작,
#   FFMPEG_TIMEOUT=abc/0 시 경고 로그 + 30초 기본값 적용.
set -u

if [ "${SPEC_TDD_ALLOW_MUTATING:-0}" != "1" ]; then
  echo "SKIPPED: requires launching throwaway containers with altered env."
  echo "--- read-only note: production container sets env explicitly ---"
  docker exec sentinel-cctv-adapter env | grep -E 'CAMERAS|STREAMING|WEB_BACKEND|FFMPEG' || true
  exit 2
fi

IMG=$(docker inspect sentinel-cctv-adapter --format '{{.Config.Image}}')
# (1) 전 변수 미설정
docker run -d --rm --name spec-cctv-k1 --network sentinel_sentinel-net "$IMG" >/dev/null
# (2) FFMPEG_TIMEOUT=abc
docker run -d --rm --name spec-cctv-k2 --network sentinel_sentinel-net -e FFMPEG_TIMEOUT=abc "$IMG" >/dev/null
trap 'docker rm -f spec-cctv-k1 spec-cctv-k2 >/dev/null 2>&1' EXIT
sleep 3
H1=$(docker exec spec-cctv-k1 wget -qO- http://localhost:8080/healthz)
L2=$(docker logs spec-cctv-k2 2>&1 | grep -i -E 'FFMPEG_TIMEOUT|warn' | head -3)
echo "k1 healthz: $H1"; echo "k2 logs: $L2"
printf '%s' "$H1" | jq -e '.status=="ok"' >/dev/null && [ -n "$L2" ] \
  && { echo "OK"; exit 0; }
echo "NOK"
exit 1
