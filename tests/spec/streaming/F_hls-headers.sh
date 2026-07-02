#!/usr/bin/env bash
# streaming §단언 F (HLS 응답 규격): m3u8 응답이 interface-streaming §계약 2 단언 A2-1
#   (헤더·Content-Type)을 통과. 헤더 값 소유자는 A2-1 — 해당 스크립트에 위임 실행.
# READ-ONLY.
set -u
exec "$(dirname "$0")/../interface-streaming/A2-1_playlist-headers.sh"
