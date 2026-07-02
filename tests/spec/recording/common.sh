#!/usr/bin/env bash
# Shared helpers — spec TDD for docs/spec/recording.md
# PRODUCTION-SAFE: read-only observation only (GET, ls, ffprobe, docker logs).
# Mutating steps in individual tests are gated behind ALLOW_MUTATING=1
# (designer approval required — do NOT set on the production system).
set -u

REC_CONTAINER="${REC_CONTAINER:-sentinel-recording}"
REC="${REC:-http://localhost:8080}"
RECORDINGS_DIR="${RECORDINGS_DIR:-/recordings}"
ARCHIVES_DIR="${ARCHIVES_DIR:-/archives}"
FAILED=0

# Run a shell command inside the recording container (read-only usage only).
rexec() { docker exec "$REC_CONTAINER" sh -c "$1"; }

# http_get <url> [host-bodyfile] — sets STATUS and CTYPE.
# Uses busybox wget inside the container (-S prints status line + headers on
# stderr; on 4xx/5xx it prints "server returned error: HTTP/1.1 NNN ...").
http_get() {
  local url="$1" body="${2:-/dev/null}" hdr
  hdr="$(docker exec "$REC_CONTAINER" sh -c "wget -S -qO- '$url'" 2>&1 >"$body")" || true
  STATUS="$(printf '%s\n' "$hdr" | grep -oE 'HTTP/[0-9.]+ [0-9]{3}' | tail -1 | awk '{print $2}')"
  CTYPE="$(printf '%s\n' "$hdr" | grep -i 'content-type:' | tail -1 | sed 's/.*[Cc]ontent-[Tt]ype: *//' | tr -d '\r')"
}

# http_head <url> — status/headers only; body discarded inside the container
# (avoids piping large MP4s through docker exec).
http_head() {
  local url="$1" hdr
  hdr="$(docker exec "$REC_CONTAINER" sh -c "wget -S -q -O /dev/null '$url'" 2>&1)" || true
  STATUS="$(printf '%s\n' "$hdr" | grep -oE 'HTTP/[0-9.]+ [0-9]{3}' | tail -1 | awk '{print $2}')"
  CTYPE="$(printf '%s\n' "$hdr" | grep -i 'content-type:' | tail -1 | sed 's/.*[Cc]ontent-[Tt]ype: *//' | tr -d '\r')"
}

# First streamKey currently in status=="recording".
active_key() {
  rexec "wget -qO- $REC/api/status" | python3 -c 'import json,sys
for e in json.load(sys.stdin):
    if e["status"]=="recording": print(e["streamKey"]); break'
}

ok()   { echo "  [ok]  $*"; }
nok()  { echo "  [NOK] $*"; FAILED=1; }
info() { echo "  [..]  $*"; }

verdict() {
  if [ "$FAILED" -eq 0 ]; then echo "VERDICT $1: OK"; else echo "VERDICT $1: NOK"; exit 1; fi
}

skip_mutating() { # skip_mutating <ID> <reason>
  echo "VERDICT $1: SKIPPED (mutating — 설계자 승인 대기) — $2"; exit 0
}
