#!/usr/bin/env bash
# youtube-adapter §단언 J (인코딩 파라미터 주입) — docs/spec/youtube-adapter.md §단언 J.
#   ENCODE_VIDEO_BITRATE→-b:v, ENCODE_GOP→-g, ENCODE_AUDIO_BITRATE→-b:a, ENCODE_PRESET→-preset.
#   미설정 시 기본값 300k/60/48k/ultrafast. 두 경우 모두 §단언 E(-c:v libx264 + -c:a aac) 성립.
#
# MUTATING: throwaway 컨테이너 2개 기동(주입/기본값) + 스트림 송출. ALLOW_MUTATING=1 필요.
#   프로덕션 오염 방지: 프로덕션 streaming 이 아닌 격리 RTMP 싱크(별도 streaming 인스턴스)로만 송출.
set -u

IMG="${YT_IMG:-sentinel-youtube-adapter:latest}"
SINK_IMG="${SINK_IMG:-sentinel-streaming:latest}"
NET="${NET:-sentinel_sentinel-net}"

skip() { echo "SKIPPED J: $*"; exit 2; }
ok()   { echo "  [ok]  $*"; }
nok()  { echo "  [NOK] $*"; FAIL=1; }
info() { echo "  [..]  $*"; }

if [ "${ALLOW_MUTATING:-0}" != "1" ]; then
  skip "(mutating — 설계자 승인 대기) throwaway 인코딩 컨테이너 기동 필요 — ALLOW_MUTATING=1 로만 실행"
fi
docker image inspect "$IMG" >/dev/null 2>&1 || skip "image $IMG 부재"

FAIL=0
TMPD=$(mktemp -d)
SINK="spec-encsink-$$"; ASET="spec-encj-set-$$"; ADEF="spec-encj-def-$$"
cleanup() { docker rm -f "$SINK" "$ASET" "$ADEF" >/dev/null 2>&1; rm -rf "$TMPD"; }
trap cleanup EXIT

# 1) 재생용 테스트 소스(3s mp4, loop) 생성 — 이미지의 ffmpeg(lavfi) 사용
docker run --rm -v "$TMPD":/media --network none "$IMG" \
  ffmpeg -hide_banner -loglevel error -f lavfi -i testsrc=size=320x240:rate=15 \
  -f lavfi -i sine=frequency=440 -t 3 -c:v libx264 -pix_fmt yuv420p -c:a aac -shortest /media/src.mp4 \
  || skip "테스트 소스 생성 실패 (ffmpeg lavfi 부재?)"

printf '[{"id":"enc","streamKey":"spec-enc-set","localFile":"/media/src.mp4"}]' > "$TMPD/cfg-set.json"
printf '[{"id":"enc","streamKey":"spec-enc-def","localFile":"/media/src.mp4"}]' > "$TMPD/cfg-def.json"

# 2) 격리 RTMP 싱크 (harmless — 프로덕션 streaming 과 무관한 throwaway 인스턴스)
docker run -d --rm --name "$SINK" --network "$NET" "$SINK_IMG" >/dev/null 2>&1 \
  && info "격리 RTMP 싱크 $SINK 기동" || info "싱크 기동 실패 — ffmpeg 재시도 창에서 ps 관측 시도"
sleep 3

# 3-a) ENCODE_* 주입 컨테이너
docker run -d --rm --name "$ASET" --network "$NET" \
  -e STREAMING_RTMP_URL="rtmp://$SINK:1935/live" \
  -e ENCODE_VIDEO_BITRATE=500k -e ENCODE_GOP=30 -e ENCODE_AUDIO_BITRATE=64k -e ENCODE_PRESET=veryfast \
  -v "$TMPD/src.mp4":/media/src.mp4:ro -v "$TMPD/cfg-set.json":/config/youtube-sources.json:ro \
  "$IMG" >/dev/null || nok "주입 컨테이너 기동 실패"

# 3-b) 기본값(미설정) 컨테이너
docker run -d --rm --name "$ADEF" --network "$NET" \
  -e STREAMING_RTMP_URL="rtmp://$SINK:1935/live" \
  -v "$TMPD/src.mp4":/media/src.mp4:ro -v "$TMPD/cfg-def.json":/config/youtube-sources.json:ro \
  "$IMG" >/dev/null || nok "기본값 컨테이너 기동 실패"

# 4) 실행 중 ffmpeg 인자 수집 (ps 프로세스 목록 + ffmpeg-cmd 로그) — ~24초 폴링
collect() { # <container>
  local c="$1" blob="" i
  for i in $(seq 1 12); do
    blob="$blob
$(docker exec "$c" ps 2>/dev/null | grep -i ffmpeg | grep -v grep)
$(docker logs "$c" 2>&1 | grep -- '-b:v' | tail -3)"
    sleep 2
  done
  printf '%s' "$blob"
}
BSET=$(collect "$ASET")
BDEF=$(collect "$ADEF")

chk() { # <label> <ere> <blob>
  if printf '%s' "$3" | grep -qE -- "$2"; then ok "$1"; else nok "$1 (미관측)"; fi
}

# ffmpeg 프로세스 자체가 관측되지 않으면 판정 근거 부재(false-green 방지 위해 명시적 NOK)
printf '%s' "$BSET" | grep -qiE 'ffmpeg|-b:v' || nok "[주입] ffmpeg 프로세스/명령 미관측 — 스트림 미구동"
printf '%s' "$BDEF" | grep -qiE 'ffmpeg|-b:v' || nok "[기본] ffmpeg 프로세스/명령 미관측 — 스트림 미구동"

echo "  -- ENCODE_* 주입 반영 --"
chk "[주입] -b:v 500k"       ' -b:v 500k( |$)'        "$BSET"
chk "[주입] -g 30"           ' -g 30( |$)'            "$BSET"
chk "[주입] -b:a 64k"        ' -b:a 64k( |$)'         "$BSET"
chk "[주입] -preset veryfast" ' -preset veryfast( |$)' "$BSET"
chk "[주입] E: -c:v libx264" ' -c:v libx264( |$)'     "$BSET"
chk "[주입] E: -c:a aac"     ' -c:a aac( |$)'         "$BSET"

echo "  -- 기본값(미설정) 불변 --"
chk "[기본] -b:v 300k"        ' -b:v 300k( |$)'        "$BDEF"
chk "[기본] -g 60"            ' -g 60( |$)'            "$BDEF"
chk "[기본] -b:a 48k"         ' -b:a 48k( |$)'         "$BDEF"
chk "[기본] -preset ultrafast" ' -preset ultrafast( |$)' "$BDEF"
chk "[기본] E: -c:v libx264"  ' -c:v libx264( |$)'     "$BDEF"
chk "[기본] E: -c:a aac"      ' -c:a aac( |$)'         "$BDEF"

if [ "$FAIL" -eq 0 ]; then echo "VERDICT J: OK"; exit 0; else echo "VERDICT J: NOK"; exit 1; fi
