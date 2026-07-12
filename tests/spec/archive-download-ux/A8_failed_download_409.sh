#!/usr/bin/env bash
# A8 — failed 아카이브의 download는 미디어를 반환하지 않는다(비-2xx, 409).
#      근거: recording.md 다운로드 계약 델타(completed만 서빙 · 그 외 비-completed 409 · 부재 404).
# 기본 SKIP: failed 아카이브를 구동하려면 스테이징 recorder 필요.
#            라이브에 failed 아카이브가 현존하면 read-only로 즉시 관측.
. "$(dirname "$0")/common.sh"
require_container A8

tmp=$(mktemp); archives_json "$tmp"
id=$(ids_with_status "$tmp" failed | head -1); rm -f "$tmp"

# 보조(항상 read-only 관측 가능): 존재하지 않는 아카이브 → 404.
http_head "$REC/api/archives/no-such-archive-spec-tdd/download"
if [ "${STATUS:-}" = "404" ]; then ok "보조: 부재 아카이브 download 404"
else nok "보조: 부재 아카이브 download status=${STATUS:-none} (404 기대)"; fi

if [ -z "$id" ]; then
  if [ "$FAILED" -ne 0 ]; then echo "VERDICT A8: NOK"; exit 1; fi
  skip_staging A8 "failed 아카이브 현존하지 않음 — 404 보조 게이트만 관측(failed-409 판정 불가)"
fi

http_head "$REC/api/archives/$id/download"
if [ "${STATUS:-}" = "200" ]; then nok "failed 아카이브($id)가 200으로 서빙됨"
elif [ -z "${STATUS:-}" ]; then nok "download 상태코드 미획득 ($id)"
else ok "failed($id) download 비-2xx: $STATUS (409 기대)"; fi
verdict A8
