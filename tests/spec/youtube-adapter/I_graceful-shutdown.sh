#!/usr/bin/env bash
# SKIP: mutating — 컨테이너 SIGTERM(정지)이 필요. 프로덕션 어댑터 정지는 라이브 스트림
#   중단을 유발 (설계자 승인 대기). 픽스처 컨테이너로 대체하더라도 push가 프로덕션
#   streaming에 스트림을 생성함.
# youtube-adapter §단언 I (정상 종료): 스트림 1개 송출 중 SIGTERM → 종료 후
#   잔존 ffmpeg 프로세스 없음.
set -u

if [ "${SPEC_TDD_ALLOW_MUTATING:-0}" != "1" ]; then
  echo "SKIPPED: requires SIGTERM to a streaming adapter container."
  exit 2
fi

IMG=$(docker inspect sentinel-youtube-adapter --format '{{.Config.Image}}')
MEDIA=$(docker inspect sentinel-youtube-adapter --format '{{range .Mounts}}{{if eq .Destination "/media"}}{{.Source}}{{end}}{{end}}')
TMPD=$(mktemp -d)
cat > "$TMPD/youtube-sources.json" <<'EOF'
[{"id":"g","youtubeUrl":"https://www.youtube.com/watch?v=placeholder","streamKey":"spec-i","localFile":"/media/yt-cam-2.mp4"}]
EOF
docker run -d --name spec-yt-i --network sentinel_sentinel-net \
  -v "$TMPD/youtube-sources.json:/config/youtube-sources.json:ro" -v "$MEDIA:/media:ro" "$IMG" >/dev/null
trap 'docker rm -f spec-yt-i >/dev/null 2>&1; rm -rf "$TMPD"' EXIT
sleep 10
docker exec spec-yt-i sh -c 'ps | grep -v grep | grep -q ffmpeg' || { echo "NOK: no ffmpeg running before SIGTERM"; exit 1; }
docker stop -t 15 spec-yt-i >/dev/null   # SIGTERM 전달
RC=$(docker inspect spec-yt-i --format '{{.State.ExitCode}}')
echo "container exit code: $RC"
# 컨테이너 종료 후 잔존 ffmpeg: 컨테이너 네임스페이스가 사라졌으므로 호스트에서 확인
LEFT=$(ps aux | grep -v grep | grep 'spec-i' | grep ffmpeg || true)
echo "leftover ffmpeg: ${LEFT:-<none>}"
[ -z "$LEFT" ] && { echo "OK"; exit 0; }
echo "NOK: leftover ffmpeg processes"
exit 1
