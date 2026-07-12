#!/usr/bin/env bash
# Isolated recording-instance harness for the §단언 P / P-2 startup-recovery gates.
#
# WHY ISOLATED: P/P-2 require ROLLING_WINDOW_MINUTES=1 and a container restart with
#   seeded non-terminal archives + seeded/absent segments. Doing that on the LIVE
#   sentinel-recording container would (a) violate the deployed 60m window the spec
#   demands be 1m, and (b) restart an actively-recording production service. So these
#   gates spin up a THROWAWAY recording container with its own anonymous volumes and
#   a 1-minute rolling window. The live stack is never touched.
#
# The isolated instance needs no streaming/web-backend: camera fetch fails gracefully
#   (empty camera set) and startup recovery (the thing under test) runs regardless.
set -u

ISO_IMG="${ISO_IMG:-sentinel-recording:latest}"
ISO_NAME="${ISO_NAME:-spec-rec-iso-$$}"
ISO_REC=/recordings
ISO_ARC=/archives

iso_exec() { docker exec "$ISO_NAME" sh -c "$1"; }

# iso_up — verify image present; caller SKIPs if not.
iso_image_ok() { docker image inspect "$ISO_IMG" >/dev/null 2>&1; }

# iso_start — launch fresh isolated instance (anonymous volumes, 1m window).
#   WEB_BACKEND_URL points at a dead port so camera fetch fails fast (no hang).
iso_start() {
  docker run -d --name "$ISO_NAME" \
    -e ROLLING_WINDOW_MINUTES=1 -e FFMPEG_TIMEOUT=60 \
    -e RECORDINGS_DIR="$ISO_REC" -e ARCHIVES_DIR="$ISO_ARC" \
    -e STREAMING_RTMP_URL="rtmp://127.0.0.1:1/live" \
    -e WEB_BACKEND_URL="http://127.0.0.1:1" \
    "$ISO_IMG" >/dev/null
}

# iso_wait_health <max_tries> — poll /healthz for {"status":"ok"} (HTTP server comes
#   up only after the ~30s camera-fetch loop, so allow generous time).
iso_wait_health() {
  local i n="${1:-45}"
  for i in $(seq 1 "$n"); do
    iso_exec "wget -qO- http://localhost:8080/healthz 2>/dev/null" 2>/dev/null | grep -q '"status":"ok"' && return 0
    sleep 2
  done
  return 1
}

iso_stop() { docker rm -f "$ISO_NAME" >/dev/null 2>&1 || true; }

# iso_make_segment_template — generate one valid 10s MPEG-TS (h264+aac) at /tmp/seg.ts
#   inside the instance, reused as the byte template for every seeded segment.
iso_make_segment_template() {
  iso_exec "ffmpeg -hide_banner -loglevel error \
    -f lavfi -i testsrc=size=320x240:rate=15 -f lavfi -i sine=frequency=440 \
    -t 10 -c:v libx264 -pix_fmt yuv420p -c:a aac -shortest -f mpegts /tmp/seg.ts"
}

# iso_seed_segment <streamKey> <YYYYMMDD_HHMMSS> — place one valid .ts at that name.
iso_seed_segment() {
  iso_exec "mkdir -p $ISO_REC/$1 && cp /tmp/seg.ts $ISO_REC/$1/$2.ts"
}

# iso_ts <relative> — UTC segment-name timestamp, e.g. iso_ts '-240 seconds'.
iso_ts() { date -u -d "$1" +%Y%m%d_%H%M%S; }
# iso_rfc3339 <relative> — UTC RFC3339 for archive from/to.
iso_rfc3339() { date -u -d "$1" +%Y-%m-%dT%H:%M:%SZ; }

# iso_write_metadata <json-array> — install a metadata.json into the instance
#   (loaded on the next restart to drive startup recovery).
iso_write_metadata() {
  local tmp; tmp=$(mktemp)
  printf '%s' "$1" > "$tmp"
  docker cp "$tmp" "$ISO_NAME:$ISO_ARC/metadata.json" >/dev/null
  rm -f "$tmp"
}

# iso_archive_field <archiveId> <field> — read one field from GET /api/archives.
iso_archive_field() {
  iso_exec "wget -qO- http://localhost:8080/api/archives 2>/dev/null" \
    | python3 -c 'import json,sys
a=[x for x in json.load(sys.stdin) if x["id"]==sys.argv[1]]
print(a[0].get(sys.argv[2],"") if a else "")' "$1" "$2" 2>/dev/null
}
