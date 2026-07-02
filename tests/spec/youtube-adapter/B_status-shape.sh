#!/usr/bin/env bash
# youtube-adapter §단언 B (상태 조회 형태): 소스 N개 config 기동 후 /api/streams/status →
#   200, 길이 N의 JSON 배열, 각 원소에 id/streamKey/status/loopCount 존재,
#   status ∈ {starting,running,error,stopped,unknown}.
# READ-ONLY: 현재 프로덕션 config가 정확히 소스 2개 — 단언의 전제와 일치.
set -u

CFG_N=$(docker exec sentinel-youtube-adapter sh -c 'cat ${YOUTUBE_CONFIG_PATH:-/config/youtube-sources.json}' | jq 'length')
OUT=$(docker exec sentinel-youtube-adapter wget -S -qO- http://localhost:8080/api/streams/status 2>&1)
echo "config N=$CFG_N"
echo "$OUT"

printf '%s\n' "$OUT" | grep -q 'HTTP/1.1 200' || { echo "NOK: not 200"; exit 1; }
BODY=$(printf '%s\n' "$OUT" | grep -v '^  ')
printf '%s' "$BODY" | jq -e "type==\"array\" and length==$CFG_N" >/dev/null \
  || { echo "NOK: not array of length $CFG_N"; exit 1; }
printf '%s' "$BODY" | jq -e 'all(.[];
    has("id") and has("streamKey") and has("status") and has("loopCount") and
    (.status | IN("starting","running","error","stopped","unknown")))' >/dev/null \
  && { echo "OK (N=$CFG_N)"; exit 0; }
echo "NOK: element shape violation"
exit 1
