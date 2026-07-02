#!/usr/bin/env bash
# 단언 I. playback 계약 — 세그먼트 존재 구간의 play?from=&to= 가
#   200 + application/vnd.apple.mpegurl, body에 EXT-X-PLAYLIST-TYPE:VOD 와 EXT-X-ENDLIST,
#   나열된 세그먼트 URL GET → 200 + video/mp2t.
#   세그먼트 없는 구간 → 404, from 누락 → 400.
# 실행 정책: READ-ONLY — 전부 실행 가능.
. "$(dirname "$0")/common.sh"

k=$(active_key)
[ -n "$k" ] || { echo "VERDICT I: NOK — 활성 스트림 없음"; exit 1; }

# 최근 완결 세그먼트가 반드시 포함되는 60초 창 (최신 세그먼트 기준 -70s ~ -10s)
latest=$(rexec "ls $RECORDINGS_DIR/$k" | grep -E '^[0-9]{8}_[0-9]{6}\.ts$' | sort | tail -1)
read -r FROM TO <<< "$(python3 - "$latest" <<'EOF'
import sys, datetime
t = datetime.datetime.strptime(sys.argv[1][:15], "%Y%m%d_%H%M%S")
f = (t - datetime.timedelta(seconds=70)).strftime("%Y-%m-%dT%H:%M:%SZ")
e = (t - datetime.timedelta(seconds=10)).strftime("%Y-%m-%dT%H:%M:%SZ")
print(f, e)
EOF
)"
info "streamKey=$k window=$FROM..$TO"

body=$(mktemp)
http_get "$REC/api/recordings/$k/play?from=$FROM&to=$TO" "$body"
[ "${STATUS:-}" = "200" ] && ok "play 200" || nok "play status=${STATUS:-none}"
case "${CTYPE:-}" in application/vnd.apple.mpegurl*) ok "Content-Type: $CTYPE";; *) nok "Content-Type=$CTYPE";; esac
grep -q '#EXT-X-PLAYLIST-TYPE:VOD' "$body" && ok "#EXT-X-PLAYLIST-TYPE:VOD 존재" || nok "VOD 태그 없음"
grep -q '#EXT-X-ENDLIST' "$body" && ok "#EXT-X-ENDLIST 존재" || nok "ENDLIST 태그 없음"

seg=$(grep -v '^#' "$body" | grep -m1 '\.ts')
if [ -z "$seg" ]; then
  nok "playlist에 세그먼트 URI 없음"
else
  case "$seg" in
    http*) url="$seg";;
    /*)    url="$REC$seg";;
    *)     url="$REC/api/recordings/$k/segments/$seg";;
  esac
  http_head "$url"
  [ "${STATUS:-}" = "200" ] && ok "세그먼트 URI GET 200 ($seg)" || nok "세그먼트 URI status=${STATUS:-none} ($url)"
  case "${CTYPE:-}" in video/mp2t*|video/MP2T*) ok "세그먼트 Content-Type: $CTYPE";; *) nok "세그먼트 Content-Type=$CTYPE";; esac
fi
rm -f "$body"

# 세그먼트가 전혀 없는 구간 → 404
http_get "$REC/api/recordings/$k/play?from=2020-01-01T00:00:00Z&to=2020-01-01T00:01:00Z"
[ "${STATUS:-}" = "404" ] && ok "빈 구간 404" || nok "빈 구간 status=${STATUS:-none} (404 기대)"

# from 누락 → 400
http_get "$REC/api/recordings/$k/play?to=$TO"
[ "${STATUS:-}" = "400" ] && ok "from 누락 400" || nok "from 누락 status=${STATUS:-none} (400 기대)"

# from 형식 오류 → 400
http_get "$REC/api/recordings/$k/play?from=not-a-time&to=$TO"
[ "${STATUS:-}" = "400" ] && ok "from 형식오류 400" || nok "from 형식오류 status=${STATUS:-none} (400 기대)"

verdict I
