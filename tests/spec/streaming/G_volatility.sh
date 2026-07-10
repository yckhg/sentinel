#!/usr/bin/env bash
# streaming §단언 G (휘발성): /tmp/hls 는 tmpfs 로 마운트되어 재시작·재생성 양쪽에서
#   휘발한다 — 죽은 스트림의 잔재 playlist 가 살아남아 거짓 active:true 를 만들지 못한다.
#
# 이전 버전 결함(round1): `docker compose up -d --force-recreate` + `/api/streams == []` 는
#   tmpfs 없이도 통과했다 — force-recreate 는 tmpfs 여부와 무관하게 새 writable layer 를 주고,
#   ==[] 는 어댑터 재접속 이전의 짧은 빈 창을 볼 뿐 tmpfs 속성을 판별하지 못한다.
# 본 게이트는 compose 경로에 의존하지 않는(실행 중 컨테이너 대상) 두 판별자로 실증한다:
#   (a) 직접 마운트 단언: /tmp/hls 가 tmpfs 인가 (근본 원인 판별자).
#   (b) restart-wipe 행동 판별자: 거짓 active 를 유발할 stale playlist 를 심고,
#       `docker restart` 후 그것이 사라지는가 (tmpfs → wipe, 아니면 → 잔존).
#
# NOTE: 이 스택에는 실 스트림(yt-cam-*)이 상시 push 되고 있어 전체 `/api/streams==[]` 는
#   올바른 tmpfs 배포에서도 성립하지 않는다(거짓 NOK). 그래서 판별은 심은 키(stale-fake)에
#   한정해 "restart 후 stale-fake 가 /api/streams 에서 사라지고 파일도 없어야 한다"로 강화한다.
#   이는 전체-빈-배열보다 엄격하고 실 스트림에 견고하며 tmpfs 를 진짜로 판별한다.
#
# mutating: 컨테이너 restart(스트리밍 순단) + 컨테이너 내부에 임시 파일 심기. TEST 스택 한정.
set -u

CT=sentinel-streaming
API_FROM=sentinel-cctv-adapter
FAKE=stale-fake
PL=/tmp/hls/${FAKE}/index.m3u8

if [ "${SPEC_TDD_ALLOW_MUTATING:-0}" != "1" ]; then
  echo "SKIPPED: restart of running streaming + planting stale playlist required. Set SPEC_TDD_ALLOW_MUTATING=1 to run."
  exit 2
fi

api_streams() { docker exec "$API_FROM" wget -qO- http://streaming:8080/api/streams; }

wait_healthy() {
  local t=0
  while [ "$t" -lt 90 ]; do
    st=$(docker inspect -f '{{if .State.Health}}{{.State.Health.Status}}{{else}}none{{end}}' "$CT" 2>/dev/null || echo unknown)
    [ "$st" = "healthy" ] && return 0
    sleep 2; t=$((t+2))
  done
  return 1
}

cleanup() { docker exec "$CT" sh -c "rm -rf /tmp/hls/${FAKE}" >/dev/null 2>&1 || true; }
trap cleanup EXIT

# --- (a) 직접 마운트 단언: /tmp/hls 는 tmpfs 여야 한다 (근본 원인 판별자) ---
echo "=== (a) mount assertion on ${CT}:/tmp/hls ==="
MNT=$(docker exec "$CT" sh -c 'grep " /tmp/hls " /proc/mounts' 2>/dev/null)
echo "proc/mounts: ${MNT:-<none>}"
if ! printf '%s' "$MNT" | awk '{print $3}' | grep -qx tmpfs; then
  echo "NOK(a): /tmp/hls is NOT a tmpfs mount — volatility not guaranteed"
  exit 1
fi
echo "OK(a): /tmp/hls is tmpfs"

# --- (b) restart-wipe 행동 판별자 ---
echo "=== (b) restart-wipe discriminator ==="
# 심기: fresh mtime → active:true 로 표면화되어야 하는 stale playlist
docker exec "$CT" sh -c "mkdir -p /tmp/hls/${FAKE} && printf '#EXTM3U\n#EXT-X-VERSION:3\n#EXT-X-TARGETDURATION:2\n#EXTINF:2.0,\nseg0.ts\n' > ${PL}"
docker exec "$CT" sh -c "test -f ${PL}" || { echo "NOK: failed to plant ${PL}"; exit 1; }
echo "planted ${PL}"
BEFORE=$(api_streams)
echo "before-restart /api/streams (stale-fake row): $(printf '%s' "$BEFORE" | jq -c ".[]|select(.streamKey==\"${FAKE}\")")"
printf '%s' "$BEFORE" | jq -e ".[]|select(.streamKey==\"${FAKE}\" and .active==true)" >/dev/null \
  || { echo "NOK: planted stale playlist did not surface as active:true (false-alive mechanism precondition unmet)"; exit 1; }
echo "confirmed: stale playlist surfaces as active:true before restart"

# restart (compose 경로 비의존)
echo "restarting ${CT} ..."
docker restart "$CT" >/dev/null
if ! wait_healthy; then echo "NOK: ${CT} did not become healthy after restart"; exit 1; fi
echo "container healthy again"

# 파일이 사라졌는가 (mtime 창과 무관한 판별자)
if docker exec "$CT" sh -c "test -e ${PL}"; then
  echo "NOK: planted ${PL} SURVIVED restart — /tmp/hls is not volatile (tmpfs absent)"
  exit 1
fi
echo "file gone: ${PL} wiped by restart"

# /api/streams 에서 stale-fake 가 사라졌는가
AFTER=$(api_streams)
echo "after-restart /api/streams: $AFTER"
if printf '%s' "$AFTER" | jq -e ".[]|select(.streamKey==\"${FAKE}\")" >/dev/null; then
  echo "NOK: stale-fake still present in /api/streams after restart — false-alive persists"
  exit 1
fi
echo "OK(b): stale-fake gone from /api/streams and filesystem after restart"

echo "OK: /tmp/hls volatility verified (tmpfs mount + restart-wipe of planted stale playlist)"
exit 0
