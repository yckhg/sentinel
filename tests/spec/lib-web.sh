# 공용 헬퍼 — web 계층 spec-tdd (interface-web-api / web-backend / web-frontend)
# 프로덕션 안전 원칙: 기본 read-only. mutating 본문은 ALLOW_MUTATING=1 없이는 실행되지 않는다.
# 모든 네트워크 호출은 Docker 컨테이너 안에서 수행한다 (호스트 설치/직접 호출 금지).

NET=${NET:-sentinel_sentinel-net}
CURL_IMG=${CURL_IMG:-curlimages/curl:latest}
PY_IMG=${PY_IMG:-python:3.12-alpine}
BACKEND=${BACKEND:-http://web-backend:8080}
FRONTEND=${FRONTEND:-http://web-frontend:80}
HWGW=${HWGW:-http://hw-gateway:8080}
SPEC_TMP=${SPEC_TMP:-/tmp/sentinel-spec-tdd}
mkdir -p "$SPEC_TMP"

# in-network curl (본문 출력)
bcurl() { docker run --rm --network "$NET" "$CURL_IMG" -sS --max-time 10 "$@"; }
# in-network curl (상태코드만)
bcode() { docker run --rm --network "$NET" "$CURL_IMG" -s -o /dev/null -w '%{http_code}' --max-time 10 "$@"; }
# in-network curl (헤더만)
bhead() { docker run --rm --network "$NET" "$CURL_IMG" -sSI --max-time 10 "$@"; }
# 본문 + 마지막 줄에 상태코드
bcurl_code() { docker run --rm --network "$NET" "$CURL_IMG" -s --max-time 10 -w '\n%{http_code}' "$@"; }

# SQLite read-only 조회: 볼륨을 :ro로 마운트하고 컨테이너 /tmp로 복사한 사본만 연다
# (프로덕션 DB 파일에는 어떤 쓰기도 발생하지 않음 — WAL 복구는 사본에서 수행)
db_query() {
  docker run --rm -v sentinel_db-data:/data:ro "$PY_IMG" python -c '
import sqlite3, sys, shutil
for f in ("sentinel.db", "sentinel.db-wal", "sentinel.db-shm"):
    try: shutil.copy("/data/" + f, "/tmp/" + f)
    except FileNotFoundError: pass
con = sqlite3.connect("/tmp/sentinel.db")
for row in con.execute(sys.argv[1]):
    print("|".join("" if v is None else str(v) for v in row))
' "$1"
}

# admin 토큰 (캐시 우선 — 캐시 미스 시에만 로그인 1회; rate limit 10/min/IP 보호)
# 실패도 캐시한다 — fixture 부재 시 로그인 재시도로 rate limit을 소진하지 않도록.
# ADMIN_TOKEN env 직접 주입 시 그것을 최우선 사용.
get_token() {
  if [ -n "${ADMIN_TOKEN:-}" ]; then printf '%s' "$ADMIN_TOKEN"; return 0; fi
  local tok_file="$SPEC_TMP/admin.token" fail_file="$SPEC_TMP/admin.token.fail"
  if [ -s "$tok_file" ]; then cat "$tok_file"; return 0; fi
  if [ -f "$fail_file" ]; then return 1; fi
  local user=${ADMIN_USERNAME:-admin} pass=${ADMIN_PASSWORD:-sentinel1234}
  local resp tok
  resp=$(bcurl -X POST "$BACKEND/auth/login" -H 'Content-Type: application/json' \
    -d "{\"username\":\"$user\",\"password\":\"$pass\"}")
  tok=$(printf '%s' "$resp" | jq -r '.token // empty' 2>/dev/null)
  if [ -z "$tok" ]; then echo "TOKEN_FAIL: $resp" >&2; touch "$fail_file"; return 1; fi
  printf '%s' "$tok" >"$tok_file"
  printf '%s' "$tok"
}

# WebSocket 관찰 클라이언트 (raw, 순수 표준 라이브러리) — tests/spec/lib/ws_client.py
# 사용: ws_observe "<path+query>" <timeout_sec> <mode: normal|noping>
ws_observe() {
  local libdir
  libdir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib"
  docker run --rm --network "$NET" -v "$libdir":/wslib:ro "$PY_IMG" \
    python /wslib/ws_client.py web-backend 8080 "$1" "${2:-10}" "${3:-normal}"
}

ok()   { echo "OK: $*"; exit 0; }
nok()  { echo "NOK: $*"; exit 1; }
skip() { echo "SKIPPED $*"; exit 2; }

require_mutating() {
  if [ "${ALLOW_MUTATING:-0}" != "1" ]; then
    skip "(mutating — 설계자 승인 대기): ALLOW_MUTATING=1 로만 실행"
  fi
}
