#!/usr/bin/env bash
# SKIP: mutating — URL 채택/거부 픽스처 config로 별도 기동이 필요. 채택 URL은 실제
#   yt-dlp 해석·RTMP push 시도를 유발하므로 프로덕션 streaming에 스트림을 생성함
#   (설계자 승인 대기).
# youtube-adapter §단언 C (URL 검증 입출력 쌍):
#   채택: https://www.youtube.com/watch?v=abc123 · https://youtu.be/abc123 · https://youtube.com/live/abc123
#   거부: http 스킴 · 201자 초과 · example.com 도메인 · youtube.com.evil.com 위장
set -u

if [ "${SPEC_TDD_ALLOW_MUTATING:-0}" != "1" ]; then
  echo "SKIPPED: requires throwaway container with fixture config (creates streams/push attempts)."
  exit 2
fi

TMPD=$(mktemp -d)
LONG="https://www.youtube.com/watch?v=$(head -c 200 /dev/zero | tr '\0' 'a')"
cat > "$TMPD/youtube-sources.json" <<EOF
[
 {"id":"ok1","youtubeUrl":"https://www.youtube.com/watch?v=abc123","streamKey":"spec-ok1"},
 {"id":"ok2","youtubeUrl":"https://youtu.be/abc123","streamKey":"spec-ok2"},
 {"id":"ok3","youtubeUrl":"https://youtube.com/live/abc123","streamKey":"spec-ok3"},
 {"id":"bad1","youtubeUrl":"http://youtube.com/watch?v=abc","streamKey":"spec-bad1"},
 {"id":"bad2","youtubeUrl":"$LONG","streamKey":"spec-bad2"},
 {"id":"bad3","youtubeUrl":"https://example.com/watch?v=abc","streamKey":"spec-bad3"},
 {"id":"bad4","youtubeUrl":"https://youtube.com.evil.com/watch?v=abc","streamKey":"spec-bad4"}
]
EOF
IMG=$(docker inspect sentinel-youtube-adapter --format '{{.Config.Image}}')
docker run -d --rm --name spec-yt-c --network sentinel_sentinel-net \
  -v "$TMPD/youtube-sources.json:/config/youtube-sources.json:ro" "$IMG" >/dev/null
trap 'docker rm -f spec-yt-c >/dev/null 2>&1; rm -rf "$TMPD"' EXIT
sleep 5
S=$(docker exec spec-yt-c wget -qO- http://localhost:8080/api/streams/status)
echo "status: $S"
printf '%s' "$S" | jq -e '
  ([.[].id] | index("ok1") and index("ok2") and index("ok3")) and
  ([.[].id] | (index("bad1") or index("bad2") or index("bad3") or index("bad4")) | not)' >/dev/null \
  && { echo "OK"; exit 0; }
echo "NOK: acceptance/rejection set mismatch"
exit 1
