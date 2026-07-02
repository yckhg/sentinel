#!/usr/bin/env bash
# youtube-adapter §단언 D (로컬 파일 무한 반복): localFile 소스가 60초 이상 경과 후에도
#   streaming /api/streams 에서 active:true 이고, yt-dlp가 한 번도 실행되지 않았으면 OK.
# READ-ONLY: 현재 프로덕션 소스 2개가 모두 localFile 지정 — 장기 가동 상태를 그대로 관찰.
#   (10초 픽스처 파일 대신 실운영 localFile 스트림으로 판정. "한 번도 실행 안 됨"은
#    컨테이너 로그 전체에서 yt-dlp 흔적 부재 + 현재 프로세스 목록으로 판정)
set -u

CFG=$(docker exec sentinel-youtube-adapter sh -c 'cat ${YOUTUBE_CONFIG_PATH:-/config/youtube-sources.json}')
KEY=$(printf '%s' "$CFG" | jq -r '[.[]|select(.localFile != null and .localFile != "")][0].streamKey // empty')
if [ -z "$KEY" ]; then echo "SKIPPED: no localFile source configured"; exit 2; fi
echo "localFile source streamKey=$KEY"

# 가동 시간 (>= 60초)
STARTED=$(docker exec sentinel-youtube-adapter wget -qO- http://localhost:8080/api/streams/status | jq -r ".[]|select(.streamKey==\"$KEY\").startedAt")
echo "startedAt=$STARTED (now=$(date -u +%Y-%m-%dT%H:%M:%SZ))"
AGE=$(( $(date +%s) - $(date -d "$STARTED" +%s) ))
[ "$AGE" -ge 60 ] || { echo "NOK: stream younger than 60s"; exit 1; }

# active:true
docker exec sentinel-cctv-adapter wget -qO- http://streaming:8080/api/streams | \
  jq -e ".[]|select(.streamKey==\"$KEY\").active == true" >/dev/null \
  || { echo "NOK: not active after ${AGE}s"; exit 1; }
echo "active:true after ${AGE}s"

# yt-dlp 미실행: 현재 프로세스 + 컨테이너 로그 흔적
PS=$(docker exec sentinel-youtube-adapter sh -c "ps | grep -v grep | grep yt-dlp" || true)
LOGS=$(docker logs sentinel-youtube-adapter 2>&1 | grep -i 'yt-dlp' | head -5 || true)
echo "ps(yt-dlp): ${PS:-<none>}"
echo "logs(yt-dlp): ${LOGS:-<none>}"
if [ -z "$PS" ] && [ -z "$LOGS" ]; then
  echo "OK: localFile stream active >= 60s, no yt-dlp execution observed"
  exit 0
fi
echo "NOK: yt-dlp was executed for localFile source"
exit 1
