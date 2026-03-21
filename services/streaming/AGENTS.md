# AGENTS.md — streaming

## Responsibility

HLS streaming server. Receives raw H.264 streams from cctv-adapter and serves them as HLS to clients. No transcoding — remuxing only.

## Scope

- Receive video streams from cctv-adapter
- Remux H.264 to HLS (m3u8 + ts segments) without transcoding
- Serve multiple camera streams simultaneously
- Provide HLS URL list to web-backend
- Always running (continuous operation)

## Interfaces

### Inbound
| Source | Method | Description |
|--------|--------|-------------|
| cctv-adapter | Stream push | Raw H.264 video streams |
| web-backend | HTTP GET `/api/streams` | Query available HLS URLs |

### Outbound
| Target | Method | Description |
|--------|--------|-------------|
| Client (via web-frontend) | HLS (HTTP) | m3u8 playlist + ts segments |

## Implementation Notes

- **NO TRANSCODING** — Remux only (H.264 -> HLS container). CPU usage must stay minimal.
- HLS segment duration should be short (2-3s) for low latency
- Must handle multiple simultaneous camera streams
- Clean up old segments to prevent disk fill
- Stream URLs should follow a predictable pattern: `/live/{cameraId}/index.m3u8`
