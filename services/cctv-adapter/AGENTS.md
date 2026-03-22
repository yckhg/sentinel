# AGENTS.md — cctv-adapter

## Responsibility

Adapter for RTSP cameras. Connects to CCTV cameras via RTSP and pushes streams to the streaming server via RTMP. Conforms to the streaming server's RTMP Input Specification (see `services/streaming/AGENTS.md`).

## Adapter Pattern

This is the **reference implementation** of the adapter pattern. See [Adapter Checklist](../../docs/adapter-checklist.md) for the full step-by-step guide to adding a new adapter. New adapters for different source types should follow the same structure:
1. Read source config (JSON file or DB)
2. Connect to source (RTSP, file, URL, etc.)
3. Convert to streaming server's RTMP input spec (H.264 no B-frames + AAC)
4. Push to `rtmp://streaming:1935/live/{streamKey}`
5. Handle reconnection on failure
6. Health endpoint at `/healthz`

**Key rule**: Adapters are responsible for making their source compatible with the streaming server's input spec. See `services/streaming/AGENTS.md` → "RTMP Input Specification" for exact requirements.

## Interfaces

### Inbound
| Source | Method | Description |
|--------|--------|-------------|
| CCTV cameras | RTSP | Raw H.264 video streams |
| web-backend | POST `/api/cameras/reload` | Trigger config reload |

### Outbound
| Target | Method | Description |
|--------|--------|-------------|
| streaming | RTMP push to `:1935/live/{streamKey}` | H.264+AAC FLV stream |

## Implementation Notes

- Uses FFmpeg: `ffmpeg -c copy -f flv` (most industrial CCTV cameras output baseline H.264, no B-frames)
- If a camera outputs B-frames, must switch to `-c:v libx264 -tune zerolatency -bf 0`
- Camera config from JSON at `CAMERAS_CONFIG_PATH` env var
- RTMP destination: `STREAMING_RTMP_URL` env var
- Each camera gets its own FFmpeg process with independent lifecycle
- Auto-reconnect with exponential backoff (1s→30s max)
- Hot reload via POST `/api/cameras/reload` (fetches camera list from web-backend)
- FFmpeg watchdog: `FFMPEG_TIMEOUT` env var (default 30s) kills hung processes
