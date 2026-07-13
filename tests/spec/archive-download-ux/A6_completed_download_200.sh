#!/usr/bin/env bash
# A6 (핵심) — completed 아카이브의 download는 2xx + Content-Type: video/mp4 +
#             비어있지 않은 본문을 반환한다.
# 기본 SKIP: completed 아카이브를 실제 구동하려면 스테이징 recorder 필요.
#            라이브에 completed 아카이브가 현존하면 read-only로 즉시 관측.
. "$(dirname "$0")/common.sh"
require_container A6

tmp=$(mktemp); archives_json "$tmp"
id=$(ids_with_status "$tmp" completed | head -1); rm -f "$tmp"
if [ -z "$id" ]; then
  skip_staging A6 "completed 아카이브 현존하지 않음 — 판정 불가"
fi

http_head "$REC/api/archives/$id/download"
case "${STATUS:-}" in
  2*) ok "completed($id) download 2xx: $STATUS";;
  *)  nok "completed($id) download status=${STATUS:-none} (2xx 기대)";;
esac
case "${CTYPE:-}" in
  video/mp4*) ok "Content-Type video/mp4";;
  *) nok "Content-Type=${CTYPE:-none} (video/mp4 기대)";;
esac
# 비어있지 않은 본문: 컨테이너 내부에서 바이트 수 확인(대용량 MP4는 host로 파이프하지 않음).
bytes=$(rexec "wget -qO- $REC/api/archives/$id/download | wc -c" 2>/dev/null | tr -d ' ')
if [ -n "$bytes" ] && [ "$bytes" -gt 0 ] 2>/dev/null; then ok "본문 non-empty: ${bytes} bytes"
else nok "본문 크기=${bytes:-none} (>0 기대)"; fi
verdict A6
