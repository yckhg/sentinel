#!/usr/bin/env bash
# A2 (핵심) — 방금 생성 요청한 아카이브는 목록에서 not-completed(status != "completed")로
#             관측된다(미완료 4종 + failed 모두 포함, 다운로드 게이트와 일관).
# 전제(mutating 게이트): 갓 생성 대상 항목 확보에 아카이브 생성 POST 필수.
#   ALLOW_MUTATING/SPEC_TDD_ALLOW_MUTATING 없으면 판정 불가 → SKIP.
. "$(dirname "$0")/common.sh"
require_container A2

if [ "$ALLOW_MUTATING" != "1" ]; then
  skip_mutating A2 "아카이브 생성 POST(/api/archives) 없이는 갓 생성 항목을 확보할 수 없음"
fi

# ---- MUTATING PART (승인 시에만) ----
key="${A2_STREAM_KEY:-$(rexec "wget -qO- $REC/api/status" | python3 -c 'import json,sys
try:
  for e in json.load(sys.stdin):
    if e.get("status")=="recording": print(e["streamKey"]); break
except Exception: pass')}"
if [ -z "$key" ]; then
  echo "VERDICT A2: SKIPPED (mutating 승인됨이나 recording 상태 streamKey 부재 — 생성 대상 없음)"; exit 0
fi

now=$(date -u +%Y-%m-%dT%H:%M:%SZ)
from=$(date -u -d '2 minutes ago' +%Y-%m-%dT%H:%M:%SZ 2>/dev/null || date -u -v-2M +%Y-%m-%dT%H:%M:%SZ)
body="{\"streamKeys\":[\"$key\"],\"from\":\"$from\",\"to\":\"$now\"}"
echo "  [!!] MUTATING: POST /api/archives $body"
resp=$(rexec "wget -qO- --post-data='$body' --header='Content-Type: application/json' $REC/api/archives")
newid=$(printf '%s' "$resp" | python3 -c 'import json,sys
try: print((json.load(sys.stdin).get("archives") or [{}])[0].get("id",""))
except Exception: print("")')
if [ -z "$newid" ]; then nok "생성 응답에서 archiveId 확보 실패: $resp"; verdict A2; fi

tmp=$(mktemp); archives_json "$tmp"
st=$(python3 -c "import json;print(next((x['status'] for x in json.load(open('$tmp')) if x['id']=='$newid'),''))")
rm -f "$tmp"
if [ "$st" = "completed" ]; then nok "갓 생성 아카이브가 곧바로 completed로 관측됨 ($newid)"
elif [ -z "$st" ]; then nok "갓 생성 아카이브가 목록에 없음 ($newid)"
else ok "갓 생성 아카이브 not-completed 관측: status=$st"; fi
verdict A2
