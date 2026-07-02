#!/usr/bin/env bash
# 단언 J. 경로 탈출 차단 — segments/{file}에서 디코드 후 `..`/`/`/`\` 포함
#   또는 비-.ts 확장자는 400이며 파일 내용이 유출되지 않는다.
# 실행 정책: READ-ONLY (거부되어야 할 GET 요청만 보냄) — 전부 실행 가능.
. "$(dirname "$0")/common.sh"

k=$(active_key)
[ -n "$k" ] || { echo "VERDICT J: NOK — 활성 스트림 없음"; exit 1; }

try() { # try <경로조각> <설명>
  local frag="$1" desc="$2" body; body=$(mktemp)
  http_get "$REC/api/recordings/$k/segments/$frag" "$body"
  if [ "${STATUS:-}" = "400" ]; then
    ok "$desc → 400"
  elif [ "${STATUS:-}" = "200" ]; then
    nok "$desc → 200 (유출! body $(wc -c < "$body") bytes)"
  else
    nok "$desc → ${STATUS:-none} (스펙은 400)"
  fi
  # 유출 검사: 어떤 응답이든 metadata/아카이브 내용이 담기면 안 됨
  if grep -q '"incidentId"' "$body" 2>/dev/null; then nok "$desc → metadata.json 내용 유출"; fi
  rm -f "$body"
}

real_other=$(rexec "ls $RECORDINGS_DIR" | grep -v "^$k$" | head -1)
real_seg=""
[ -n "$real_other" ] && real_seg=$(rexec "ls $RECORDINGS_DIR/$real_other 2>/dev/null" | grep -m1 '\.ts$' || true)

try "..%2Fmetadata.json"                          "..%2Fmetadata.json (상위 탈출+비ts)"
try "..%2F..%2Farchives%2Fmetadata.json"          "..%2F..%2Farchives%2Fmetadata.json (아카이브 메타 탈출)"
try "%2E%2E%2Fmetadata.json"                      "%2E%2E%2F 이중 인코딩 변형"
try "foo.mp4"                                     "비-.ts 확장자 (foo.mp4)"
try "..%5Cfoo.ts"                                 "백슬래시 탈출 (..%5Cfoo.ts)"
if [ -n "$real_seg" ]; then
  try "..%2F$real_other%2F$real_seg"              "타 스트림 실존 세그먼트 탈출 (..%2F$real_other%2F$real_seg)"
fi

verdict J
