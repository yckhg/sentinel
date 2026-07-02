#!/usr/bin/env bash
# A2-4 (세그먼트 자동 정리): 장시간 push 중인 스트림 디렉토리의 .ts 파일 수가
#   playlist 길이 상응 수준(≤ 10개)으로 유지되면 OK. 단조 증가하면 NOK.
# READ-ONLY: 컨테이너 내부 디렉토리 관찰만 수행 (현재 스트림은 수일째 push 중 —
#   5분 push 조건을 초과 충족).
set -u

KEY="${STREAM_KEY:-$(docker exec sentinel-cctv-adapter wget -qO- http://streaming:8080/api/streams | jq -r '[.[]|select(.active)][0].streamKey // empty')}"
if [ -z "$KEY" ]; then echo "NOK: no active stream to observe"; exit 1; fi
echo "observing streamKey=$KEY"

C1=$(docker exec sentinel-streaming sh -c "ls /tmp/hls/${KEY}/*.ts 2>/dev/null | wc -l")
sleep 20
C2=$(docker exec sentinel-streaming sh -c "ls /tmp/hls/${KEY}/*.ts 2>/dev/null | wc -l")
echo "ts count: t0=$C1, t+20s=$C2"
docker exec sentinel-streaming sh -c "ls -la /tmp/hls/${KEY}/ | tail -8"

if [ "$C1" -le 10 ] && [ "$C2" -le 10 ]; then
  echo "OK: segment count bounded (<=10) on a long-running stream"
  exit 0
fi
echo "NOK: segment count exceeds bound (t0=$C1, t+20s=$C2)"
exit 1
