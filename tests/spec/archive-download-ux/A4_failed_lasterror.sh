#!/usr/bin/env bash
# A4 (핵심) — status=="failed" 모든 항목은 비어있지 않은 실패 사유 lastError를 가진다
#             (finalize-직접실패 + recovery 실패 등 모든 failed 종단 전이 순회).
# READ-ONLY. vacuity 가드: failed 항목이 없으면 판정 불가(전제 미충족).
. "$(dirname "$0")/common.sh"
require_container A4

tmp=$(mktemp); archives_json "$tmp"
python3 -c "
import json,sys
data=json.load(open('$tmp'))
failed=[x for x in data if x.get('status')=='failed']
if not failed:
    print('  [..]  failed 아카이브 없음 — 판정 불가(전제 미충족, vacuity 가드)'); sys.exit(2)
rc=0
for x in failed:
    le=x.get('lastError')
    if isinstance(le,str) and le.strip():
        print('  [ok]  %s lastError=%r'%(x['id'],le))
    else:
        # 델타 미착지 시 필드명이 'error'일 수 있음 — 명시적으로 구분해 NOK.
        print('  [NOK] failed 항목 %s 에 non-empty lastError 없음 (error=%r)'%(x['id'],x.get('error'))); rc=1
sys.exit(rc)
"
rc=$?; rm -f "$tmp"
case "$rc" in
  0) echo "VERDICT A4: OK";;
  2) echo "VERDICT A4: SKIPPED (전제 미충족 — failed 아카이브 없음)";;
  *) echo "VERDICT A4: NOK"; exit 1;;
esac
