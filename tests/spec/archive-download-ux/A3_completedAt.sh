#!/usr/bin/env bash
# A3 (핵심) — status=="completed" 항목은 non-null completedAt(RFC3339, UTC)을 가지며
#             미완료 항목은 completedAt이 null/부재다.
# 전제: completedAt 델타가 recording SSOT+코드에 착지한 이후에만 판정 가능.
#       또한 completed 아카이브를 실제로 구동하려면 스테이징 recorder(±) 필요.
#       레거시(델타 이전 생성) completed는 판정 대상에서 제외.
. "$(dirname "$0")/common.sh"
require_container A3

tmp=$(mktemp); archives_json "$tmp"
# 델타 착지 여부 = 어떤 completed 항목이 completedAt 키를 담는가로 관측.
delta_landed=$(python3 -c "
import json
data=json.load(open('$tmp'))
comp=[x for x in data if x.get('status')=='completed']
print('yes' if any(('completedAt' in x) for x in comp) else 'no')
" 2>/dev/null)

if [ "$delta_landed" != "yes" ]; then
  rm -f "$tmp"
  skip_delta A3 "completed 항목에 completedAt 필드 부재 — 델타 미착지 또는 completed fixture 없음(스테이징 recorder 필요)"
fi

python3 -c "
import json,sys,datetime
data=json.load(open('$tmp'))
rc=0; checked=0
for x in data:
    if x.get('status')=='completed' and 'completedAt' in x and x['completedAt']:
        checked+=1
        v=x['completedAt']
        try:
            dt=datetime.datetime.fromisoformat(v.replace('Z','+00:00'))
        except Exception:
            print('  [NOK] completedAt 파싱 불가: %r (%s)'%(v,x['id'])); rc=1; continue
        if dt.utcoffset() != datetime.timedelta(0):
            print('  [NOK] completedAt UTC 아님: %r (%s)'%(v,x['id'])); rc=1
        else:
            print('  [ok]  %s completedAt=%s (RFC3339/UTC)'%(x['id'],v))
    elif x.get('status')!='completed':
        if x.get('completedAt') not in (None,''):
            print('  [NOK] 미완료 항목이 completedAt 보유: %s status=%s'%(x['id'],x['status'])); rc=1
if checked==0:
    print('  [..]  completed(델타 이후) 대상 항목 없음 — 판정 불가'); sys.exit(2)
sys.exit(rc)
"
rc=$?; rm -f "$tmp"
case "$rc" in
  0) echo "VERDICT A3: OK";;
  2) skip_staging A3 "델타는 착지했으나 판정할 completed 아카이브 없음";;
  *) echo "VERDICT A3: NOK"; exit 1;;
esac
