#!/usr/bin/env bash
# SKIP: mutating — URL 채택/거부 픽스처 config로 별도 기동이 필요 (설계자 승인 대기).
# youtube-adapter §단언 C (URL 검증 입출력 쌍):
#   채택: https://www.youtube.com/watch?v=abc123 · https://youtu.be/abc123 · https://youtube.com/live/abc123
#   거부: http 스킴 · 201자 초과 · example.com 도메인 · youtube.com.evil.com 위장
#
# 격리 규율(J/J-2 정합): 채택 판정은 status 목록 멤버십으로만 하므로 실제 push 불요.
#   따라서 `--network none` + STREAMING_RTMP_URL 도달 불가로 기동해 프로덕션 streaming
#   (rtmp://streaming:1935/live)·외부 yt-dlp 를 일절 접촉하지 않는다. 채택 소스는 streams
#   맵에 등재되어(startStream) status 목록에 id 로 나타나고(해석 실패해도 등재 유지),
#   거부 소스는 loadConfig 에서 skip 되어 목록에 미등장 — 멤버십만으로 판정 가능.
set -u

# 게이트 변수 일관화: SPEC_TDD_ALLOW_MUTATING(스트리밍군 정본) 또는 ALLOW_MUTATING 중
#   하나라도 1 이면 youtube 게이트군(C/F/G/J/J-2)이 함께 켜진다(조용한 부분초록 방지).
if [ "${SPEC_TDD_ALLOW_MUTATING:-0}" != "1" ] && [ "${ALLOW_MUTATING:-0}" != "1" ]; then
  echo "SKIPPED: requires throwaway isolated container with fixture config. Set SPEC_TDD_ALLOW_MUTATING=1 to run."
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
# 격리 기동: --network none (프로덕션 streaming/외부 yt-dlp 미접촉) +
#   STREAMING_RTMP_URL 도달 불가(방어적, network none 이라 어차피 도달 불가).
#   HTTP 서버는 loopback 바인딩이므로 network none 에서도 기동하며, StartAll 은
#   비블로킹(고루틴) 후 곧바로 ListenAndServe → status 목록이 즉시 노출됨.
docker run -d --rm --name spec-yt-c --network none \
  -e STREAMING_RTMP_URL="rtmp://127.0.0.1:1/live" \
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
