#!/usr/bin/env bash
# G2. 위조 헤더 무시 (신뢰 경계, 내부-실행형): /api/send-email 의 내부/외부 판정은 소켓
#     원격 주소(연결 상대)에만 의존하며, 클라이언트가 제어 가능한 X-Forwarded-For 헤더로는
#     뒤집을 수 없다. 내부망 컨테이너에서 XFF 에 공인 IP(8.8.8.8)를 위조해 POST해도 403 이
#     되지 않아야 한다(SMTP 미설정=503 / 설정=200·이후 발송 — 어느 쪽이든 403 아님).
# spec: docs/spec/notifier.md — 검증 단언 G2 (§출력 10 신뢰 경계, 입력 §2)
# 판정: OK=exit0 (코드 != 403), NOK=exit1 (403 — 위조 XFF로 외부 판정 뒤집힘), SKIPPED=exit2.
set -uo pipefail
. "$(dirname "$0")/../lib-web.sh"
NOTIFIER=${NOTIFIER:-http://notifier:8080}

# /api/send-email 은 SMTP 설정 시 실제 메일 발송을 유발할 수 있어 mutating 으로 분류
# (기존 g_email_access_control.sh 와 동일 취급). 스모크(ALLOW_MUTATING=0)에서는 SKIP.
require_mutating

# 위조 XFF 를 담아 내부망(소켓 원격 주소=사설망)에서 호출. IP 판정은 send-email 핸들러의
# 최초 단계이므로 SMTP 설정 여부와 무관하게 여기서 403/비403 이 갈린다.
code=$(bcode -X POST "$NOTIFIER/api/send-email" \
  -H 'Content-Type: application/json' \
  -H 'X-Forwarded-For: 8.8.8.8' \
  -d '{"to":"a@b.c","subject":"spec G2 forged-xff","body":"y"}')
echo "code=$code (기대: != 403; SMTP 미설정 503 / 설정 200)"

# 위조 XFF 로 인해 '외부'로 오판되어 403 이 나오면 신뢰 경계 위반 → NOK.
if [ "$code" = 403 ]; then
  nok "위조 X-Forwarded-For(8.8.8.8)로 내부 판정이 뒤집혀 403(외부 거절) — IP 판정이 XFF 를 신뢰함"
fi
ok "위조 XFF 무시 — 소켓 원격 주소 기준 내부 판정 유지 (code=$code)"
