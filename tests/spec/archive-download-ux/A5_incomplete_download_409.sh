#!/usr/bin/env bash
# A5 (핵심) — 미완료 아카이브(protecting/pending/finalizing/processing)의 download는
#             2xx+미디어를 반환하지 않는다(비-2xx, 예 409). 부분/손상 파일 미노출.
# 기본 SKIP: 미완료행을 확보하려면 스테이징 recorder(더미 RTMP + 격리 볼륨)가 필요.
#            단, 라이브에 미완료 아카이브가 우연히 현존하면 read-only로 즉시 관측.
. "$(dirname "$0")/common.sh"
require_container A5

tmp=$(mktemp); archives_json "$tmp"
id=""
for st in protecting pending finalizing processing; do
  id=$(ids_with_status "$tmp" "$st" | head -1); [ -n "$id" ] && break
done
rm -f "$tmp"

if [ -z "$id" ]; then
  skip_staging A5 "미완료(protecting/pending/finalizing/processing) 아카이브 현존하지 않음 — 판정 불가"
fi

http_head "$REC/api/archives/$id/download"
if [ "${STATUS:-}" = "200" ]; then nok "미완료 아카이브($id, $st)가 200으로 서빙됨 (부분/손상 파일 노출)"
elif [ -z "${STATUS:-}" ]; then nok "download 상태코드 미획득 ($id)"
else ok "미완료($id) download 비-2xx: $STATUS (409 기대)"; fi
verdict A5
