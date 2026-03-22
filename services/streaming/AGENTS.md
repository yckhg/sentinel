# AGENTS.md — streaming

## Responsibility

HLS streaming server. Receives raw H.264 streams from cctv-adapter and serves them as HLS to clients. No transcoding — remuxing only.

## Scope

- Receive video streams from cctv-adapter via RTMP on port 1935
- Remux H.264 to HLS (m3u8 + ts segments) without transcoding
- Serve multiple camera streams simultaneously
- Provide HLS URL list to web-backend
- Always running (continuous operation)

## Interfaces

### Inbound
| Source | Method | Description |
|--------|--------|-------------|
| cctv-adapter | RTMP push to `:1935/live/{cameraId}` | Raw H.264 video streams |
| web-backend | HTTP GET `/api/streams` | Query available HLS URLs |

### Outbound
| Target | Method | Description |
|--------|--------|-------------|
| Client (via web-frontend) | HLS (HTTP) | m3u8 playlist + ts segments at `/live/{cameraId}/index.m3u8` |

## Architecture

Hybrid container with two processes:
- **nginx-rtmp** (port 1935 RTMP + port 8080 HTTP): Accepts RTMP streams, converts to HLS segments in `/tmp/hls/{cameraId}/`, serves HLS files at `/live/`
- **Go streaming-api** (port 8081): Serves `/healthz` and `/api/streams` (scans `/tmp/hls` for active streams)

nginx proxies `/healthz` and `/api/*` requests from :8080 to the Go binary on :8081.

## Critical Role: Single Source of Truth for Stream Status

This service is the ONLY authority on whether a camera stream is alive or dead. Adapters (cctv-adapter, youtube-adapter) push streams here, but they do NOT report status. web-backend MUST query `/api/streams` for both HLS URLs and active/inactive status. If a stream is active here, the camera is "connected". If not, it's "disconnected". No other service should be consulted for stream status.

## Implementation Notes

- **NO TRANSCODING** — Remux only (H.264 -> HLS container). CPU usage must stay minimal.
- HLS fragment: 2s, playlist length: 10s (5 segments)
- `hls_nested on` creates per-camera subdirectories under `/tmp/hls/`
- `hls_cleanup on` automatically removes old .ts segments
- Stream is considered "active" if playlist was modified within the last 15 seconds
- `startedAt` in `/api/streams` reflects playlist file mtime (proxy for stream start)
- entrypoint.sh starts Go binary in background, then nginx in foreground
