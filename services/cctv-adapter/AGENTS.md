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
- Must handle camera disconnect/reconnect gracefully
- Camera configuration (RTSP URLs, credentials) should be loaded from config
- Report "disconnected" status per camera when stream is lost
- Minimize resource usage — this runs on a mini PC
- Adding a new camera type means only modifying this service
