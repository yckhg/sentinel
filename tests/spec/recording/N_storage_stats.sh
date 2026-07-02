#!/usr/bin/env bash
# 단언 N. 저장 통계 — GET /api/storage 응답에 recordingsBytes, archivesBytes,
#   totalUsedBytes, archiveCount, diskTotalBytes, diskAvailableBytes 가 모두 존재하고 0 이상.
#   (출력 계약의 diskUsedBytes 도 보조 확인)
# 실행 정책: READ-ONLY — 실행 가능.
. "$(dirname "$0")/common.sh"

body=$(mktemp)
http_get "$REC/api/storage" "$body"
[ "${STATUS:-}" = "200" ] && ok "GET /api/storage 200" || nok "status=${STATUS:-none}"

res=$(python3 - "$body" <<'EOF'
import json, sys
d = json.load(open(sys.argv[1]))
need = ["recordingsBytes", "archivesBytes", "totalUsedBytes", "archiveCount",
        "diskTotalBytes", "diskAvailableBytes"]
missing = [k for k in need if k not in d]
neg = [k for k in need if k in d and not (isinstance(d[k], (int, float)) and d[k] >= 0)]
if missing: print("NOK 누락:", ",".join(missing))
elif neg:   print("NOK 음수/비수치:", ",".join(neg))
else:
    extra = "diskUsedBytes 존재" if "diskUsedBytes" in d else "diskUsedBytes 부재(출력 계약 항목)"
    print(f"OK 필수 6키 존재·0 이상 — {json.dumps({k:d[k] for k in need})} ({extra})")
EOF
)
rm -f "$body"
case "$res" in OK*) ok "$res";; *) nok "$res";; esac
verdict N
