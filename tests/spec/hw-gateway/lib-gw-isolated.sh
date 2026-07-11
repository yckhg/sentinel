# lib-gw-isolated.sh — throwaway isolated hw-gateway harness for negative-path gates.
#
# WHY: 500/4xx/retry/dedup-recovery paths must be exercised WITHOUT touching the
# live web-backend / notifier / broker. This lib spins up, on a throwaway docker
# network, a dedicated mosquitto broker + mock HTTP servers + an isolated
# hw-gateway (image built from services/hw-gateway), then tears everything down
# via the caller's trap.
#
# IMPORTANT — clientID collision: the product hardcodes MQTT clientID
# "sentinel-hw-gateway" (main.go). If an isolated gateway shared the LIVE broker
# it would evict the live gateway from its persistent session. So every isolated
# gateway gets its OWN broker here — the live mosquitto is never touched.
#
# Requires docker only (no host installs). Image resolution:
#   GW_ISO_IMG (default sentinel-hw-gateway-iso:test) → build with:
#     docker build -t sentinel-hw-gateway-iso:test services/hw-gateway

GW_ISO_IMG=${GW_ISO_IMG:-sentinel-hw-gateway-iso:test}
MOSQ_IMG=${MOSQ_IMG:-eclipse-mosquitto:2}
PYI_IMG=${PYI_IMG:-python:3.12-alpine}
CURL_IMG=${CURL_IMG:-curlimages/curl:latest}

ISO_TAG="gwiso$(date +%s)$$"
ISO_NET="net-$ISO_TAG"
ISO_DIR="$(mktemp -d)"
ISO_CONTAINERS=""

iso_reg() { ISO_CONTAINERS="$ISO_CONTAINERS $1"; }

# iso_cleanup — remove every container we started + the throwaway network + tmp
# fixtures. Idempotent; wire into the caller's `trap ... EXIT`.
iso_cleanup() {
  local c
  for c in $ISO_CONTAINERS; do docker rm -f "$c" >/dev/null 2>&1 || true; done
  docker network rm "$ISO_NET" >/dev/null 2>&1 || true
  rm -rf "$ISO_DIR" 2>/dev/null || true
}

# iso_init — create the throwaway network and write the broker/mock fixtures.
iso_init() {
  docker network create "$ISO_NET" >/dev/null
  cat >"$ISO_DIR/mosquitto.conf" <<'EOF'
listener 1883
allow_anonymous true
persistence true
persistence_location /mosquitto/data/
EOF
  # Mock HTTP server: replies MOCK_STATUS to every request and logs "REQ <m> <path>"
  # so the caller can count per-path hits from `docker logs`.
  cat >"$ISO_DIR/mock.py" <<'EOF'
import os, http.server
STATUS = int(os.environ.get("MOCK_STATUS", "200"))
class H(http.server.BaseHTTPRequestHandler):
    def _handle(self):
        n = int(self.headers.get("Content-Length", 0) or 0)
        if n:
            self.rfile.read(n)
        print("REQ %s %s" % (self.command, self.path), flush=True)
        self.send_response(STATUS)
        self.send_header("Content-Type", "application/json")
        self.end_headers()
        self.wfile.write(b"{}")
    def do_POST(self): self._handle()
    def do_GET(self): self._handle()
    def log_message(self, *a): pass
http.server.HTTPServer(("0.0.0.0", 8080), H).serve_forever()
EOF
}

# iso_broker <name> — start a mosquitto broker container <name> on ISO_NET.
iso_broker() {
  docker run -d --name "$1" --network "$ISO_NET" \
    -v "$ISO_DIR/mosquitto.conf":/mosquitto/config/mosquitto.conf:ro \
    "$MOSQ_IMG" >/dev/null
  iso_reg "$1"
}

# iso_broker_acl <name> <acl-file-on-host> — start a broker with an ACL file so a
# specific topic SUBSCRIBE can be denied (SUBACK 0x80) — used by F5.
iso_broker_acl() {
  local conf="$ISO_DIR/mosq-$1.conf"
  cat >"$conf" <<EOF
listener 1883
allow_anonymous true
acl_file /mosquitto/config/acl
EOF
  docker run -d --name "$1" --network "$ISO_NET" \
    -v "$conf":/mosquitto/config/mosquitto.conf:ro \
    -v "$2":/mosquitto/config/acl:ro \
    "$MOSQ_IMG" >/dev/null
  iso_reg "$1"
}

# iso_mock <name> <status> — start a mock HTTP server container returning <status>.
iso_mock() {
  docker run -d --name "$1" --network "$ISO_NET" -e MOCK_STATUS="$2" \
    -v "$ISO_DIR/mock.py":/mock.py:ro "$PYI_IMG" python /mock.py >/dev/null
  iso_reg "$1"
}

# iso_gw <name> <broker-host> <notifier-url> <web-url> [extra docker args...] —
# start an isolated gateway pointed at the given broker/mock URLs.
iso_gw() {
  local name="$1" bhost="$2" nurl="$3" wurl="$4"; shift 4
  docker run -d --name "$name" --network "$ISO_NET" \
    -e MQTT_BROKER_URL="tcp://$bhost:1883" \
    -e NOTIFIER_URL="$nurl" \
    -e WEB_BACKEND_URL="$wurl" \
    "$@" "$GW_ISO_IMG" >/dev/null
  iso_reg "$name"
}

# iso_code <container> — HTTP status of <container>'s /healthz (in-network curl).
iso_code() {
  docker run --rm --network "$ISO_NET" "$CURL_IMG" -s -o /dev/null \
    -w '%{http_code}' --max-time 3 "http://$1:8080/healthz" 2>/dev/null
}

# iso_wait_healthy <gw> [tries] — poll until /healthz==200 (connected + subscribed).
iso_wait_healthy() {
  local gw="$1" tries="${2:-40}" i
  for i in $(seq 1 "$tries"); do
    [ "$(iso_code "$gw")" = 200 ] && return 0
    sleep 1
  done
  return 1
}

# iso_pub <broker> <qos> <topic> <payload> — publish via the broker's own client.
iso_pub() {
  docker exec "$1" mosquitto_pub -h localhost -q "$2" -t "$3" -m "$4"
}

# iso_count <container> <pattern> — count request-log lines matching <pattern>.
iso_count() {
  docker logs "$1" 2>&1 | grep -c -- "$2" 2>/dev/null || true
}

# iso_preflight — ensure the isolated image exists AND is not stale.
#
# STALE-IMAGE GUARD: a pre-existing sentinel-hw-gateway-iso:test built from an
# OLDER services/hw-gateway would false-green new regressions (old binary under
# test). So we rebuild not only when the image is missing, but also when any
# source file under services/hw-gateway is newer than the image's build time.
# git checkout stamps changed files with a fresh mtime, so switching to a newer
# integration HEAD is detected. When source is unchanged the image build time is
# already >= the newest source mtime, so no rebuild fires (cheap: no docker build
# invoked at all). Only the throwaway iso image is (re)built — the live
# `sentinel-hw-gateway` image is a different tag and is never touched.
iso_preflight() {
  local src need_build=0
  src="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../../services/hw-gateway" 2>/dev/null && pwd)"
  if ! docker image inspect "$GW_ISO_IMG" >/dev/null 2>&1; then
    need_build=1
  elif [ -n "$src" ]; then
    local created img_epoch src_epoch
    created="$(docker image inspect -f '{{.Created}}' "$GW_ISO_IMG" 2>/dev/null)"
    img_epoch="$(date -d "$created" +%s 2>/dev/null || echo 0)"
    src_epoch="$(find "$src" -type f -printf '%T@\n' 2>/dev/null | sort -rn | head -1 | cut -d. -f1)"
    if [ -n "$src_epoch" ] && [ "${src_epoch:-0}" -gt "${img_epoch:-0}" ]; then
      echo "[iso] source ($src_epoch) newer than image ($img_epoch) — rebuilding $GW_ISO_IMG" >&2
      need_build=1
    fi
  fi
  if [ "$need_build" = 1 ]; then
    if [ -n "$src" ] && [ -f "$src/Dockerfile" ]; then
      echo "[iso] building $GW_ISO_IMG from $src ..." >&2
      docker build -t "$GW_ISO_IMG" "$src" >/dev/null 2>&1 || return 1
    else
      return 1
    fi
  fi
  return 0
}
