# AGENTS.md — cctv-adapter

## Responsibility

Abstracts camera types and protocols. Receives video streams from cameras and forwards them to the streaming server without any transcoding.

## Scope

- Connect to CCTV cameras via RTSP
- Forward raw H.264 streams to the streaming server
- Manage multiple camera connections simultaneously
- Provide camera health/connection status
- When cameras change, only this service needs modification

## Interfaces

### Inbound
| Source | Method | Description |
|--------|--------|-------------|
| CCTV cameras | RTSP | Raw H.264 video streams |
| web-backend | HTTP GET `/api/cameras/status` | Camera connection status query |

### Outbound
| Target | Method | Description |
|--------|--------|-------------|
| streaming | Stream push | Forward raw video to streaming server |

## Implementation Notes

- **NO TRANSCODING** — This is a hard rule. Pass streams through as-is.
- Uses FFmpeg as subprocess for RTSP→RTMP forwarding (`ffmpeg -c copy -f flv`)
- Camera config loaded from JSON file at `CAMERAS_CONFIG_PATH` env var (default `/config/cameras.json`)
- Forward destination: `STREAMING_RTMP_URL` env var (default `rtmp://streaming:1935/live`)
- Each camera gets its own FFmpeg process with independent lifecycle
- Auto-reconnect with exponential backoff (1s→30s max) on FFmpeg process exit
- Status tracked per camera: `connected`, `disconnected`, `reconnecting`
- `GET /api/cameras/status` returns copy of status data (no pointers) for thread safety
- Docker image includes FFmpeg (`apk add ffmpeg`)
- Camera changes require config file update + container restart
- Minimize resource usage — this runs on a mini PC
- Adding a new camera type means only modifying this service
