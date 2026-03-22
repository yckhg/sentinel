# AGENTS.md — streaming

## Responsibility

HLS streaming server. Receives streams via RTMP and serves them as HLS to clients. No transcoding — remuxing only. This is the central hub that all adapters push to.

## Single Source of Truth

This service is the ONLY authority on:
- **Stream status**: alive or dead (web-backend queries `/api/streams`)
- **HLS URLs**: relative paths like `/live/{streamKey}/index.m3u8`

No other service reports stream status. If it's active here, it's "connected". If not, "disconnected".

## RTMP Input Specification

Any adapter that pushes streams to this server MUST conform to this spec:

| Item | Requirement |
|------|-------------|
| Protocol | RTMP |
| Endpoint | `rtmp://streaming:1935/live/{streamKey}` |
| Video codec | H.264 Baseline or Main profile, **NO B-frames** |
| Audio codec | AAC (any profile: LC, HE-AAC) |
| Container | FLV (`-f flv`) |
| B-frames | **FORBIDDEN** — nginx-rtmp v1.2.2 drops connection after ~5s if B-frames present |
| Encoding hint | Use `-tune zerolatency` or `-bf 0` when source may contain B-frames |
| Failure symptom | "Connection reset by peer" on audio packet muxing (misleading — actual cause is video B-frames) |

New adapters: read this spec, ensure your output conforms, push to the RTMP endpoint. Done. See [Adapter Checklist](../../docs/adapter-checklist.md) for the full step-by-step guide.

## HLS Output Specification

| Item | Value |
|------|-------|
| Format | HLS (m3u8 + ts segments) |
| URL pattern | `/live/{streamKey}/index.m3u8` |
| Fragment duration | 2 seconds |
| Playlist length | 10 seconds (5 segments) |
| Cleanup | Automatic (old segments removed) |

web-backend returns these as **relative URLs** to the browser. nginx proxies `/live/` to this service.

## API

| Endpoint | Description |
|----------|-------------|
| GET `/api/streams` | Returns list of active streams with cameraId, hlsUrl, active status |
| GET `/healthz` | Health check |

## Architecture

Hybrid container with two processes:
- **nginx-rtmp** (port 1935 RTMP + port 8080 HTTP): Accepts RTMP, converts to HLS in `/tmp/hls/{streamKey}/`
- **Go streaming-api** (port 8081): Serves `/healthz` and `/api/streams`

nginx proxies `/healthz` and `/api/*` from :8080 to Go on :8081.

## Implementation Notes

- **NO TRANSCODING** — Remux only. CPU usage must stay minimal.
- `hls_nested on` creates per-stream subdirectories under `/tmp/hls/`
- `hls_cleanup on` automatically removes old .ts segments
- Stream is considered "active" if playlist was modified within the last 30 seconds
- entrypoint.sh starts Go binary in background, then nginx in foreground
