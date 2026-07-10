#!/usr/bin/env bash
# interface-streaming §계약4 A4-6 (restart 후 거짓 alive 없음):
#   /tmp/hls 는 휘발성이므로, 죽은 스트림의 잔재 playlist 는 컨테이너 restart 후 사라져야 하며
#   /api/streams 에 거짓 active:true 로 다시 나타나서는 안 된다.
# 판별 방법: fresh mtime 의 stale playlist 를 실행 중 컨테이너에 심어 그것이 active:true 로
#   표면화됨을 확인 → `docker restart sentinel-streaming`(compose 경로 비의존) → healthy 대기 →
#   심은 항목이 /api/streams 에서 사라지고(파일도 없음) 함을 단언한다.
#   tmpfs → wipe → 통과. tmpfs 부재 → 잔존 → 거짓 alive → NOK.
# mutating: restart(스트리밍 순단) + 임시 파일 심기. TEST 스택 한정.
set -u

CT=sentinel-streaming
API_FROM=sentinel-cctv-adapter
FAKE=stale-fake-a46
PL=/tmp/hls/${FAKE}/index.m3u8

if [ "${SPEC_TDD_ALLOW_MUTATING:-0}" != "1" ]; then
  echo "SKIPPED: requires restart of running streaming + planting stale playlist. Set SPEC_TDD_ALLOW_MUTATING=1 to run."
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

# 1) 거짓 alive 를 유발할 stale playlist 심기 (fresh mtime)
docker exec "$CT" sh -c "mkdir -p /tmp/hls/${FAKE} && printf '#EXTM3U\n#EXT-X-VERSION:3\n#EXT-X-TARGETDURATION:2\n#EXTINF:2.0,\nseg0.ts\n' > ${PL}"
docker exec "$CT" sh -c "test -f ${PL}" || { echo "NOK: failed to plant ${PL}"; exit 1; }
BEFORE=$(api_streams)
echo "planted; before-restart stale row: $(printf '%s' "$BEFORE" | jq -c ".[]|select(.streamKey==\"${FAKE}\")")"
printf '%s' "$BEFORE" | jq -e ".[]|select(.streamKey==\"${FAKE}\" and .active==true)" >/dev/null \
  || { echo "NOK: planted stale playlist did not surface as active:true (precondition unmet)"; exit 1; }

# 2) restart → healthy 대기
echo "restarting ${CT} ..."
docker restart "$CT" >/dev/null
if ! wait_healthy; then echo "NOK: ${CT} did not become healthy after restart"; exit 1; fi

# 3) 단언: 심은 stale 항목이 사라지고 파일도 없어야 한다
if docker exec "$CT" sh -c "test -e ${PL}"; then
  echo "NOK: planted ${PL} survived restart — false-alive would persist (tmpfs absent)"
  exit 1
fi
AFTER=$(api_streams)
echo "after-restart /api/streams: $AFTER"
if printf '%s' "$AFTER" | jq -e ".[]|select(.streamKey==\"${FAKE}\")" >/dev/null; then
  echo "NOK: ${FAKE} reappears in /api/streams after restart — false alive"
  exit 1
fi
echo "OK: no false-alive after restart (stale playlist wiped from filesystem and /api/streams)"
exit 0
