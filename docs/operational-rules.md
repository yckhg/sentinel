# Operational Rules

> 독자: 운영자 / orchestrator (정책 참조)

Rules for naming, configuration, monitoring, and resource management in Sentinel.

## 1. Container Naming Convention

All containers follow the pattern: `sentinel-{service-name}`

For adapters specifically: `sentinel-{source-type}-adapter`

| Container Name | Service |
|----------------|---------|
| `sentinel-mosquitto` | MQTT broker |
| `sentinel-hw-gateway` | Hardware gateway |
| `sentinel-cctv-adapter` | CCTV/RTSP stream adapter |
| `sentinel-youtube-adapter` | YouTube/file stream adapter |
| `sentinel-streaming` | HLS streaming server |
| `sentinel-notifier` | Alert dispatcher |
| `sentinel-web-backend` | REST API + WebSocket |
| `sentinel-web-frontend` | Mobile-first UI |

When adding a new adapter, follow the same pattern: `sentinel-<source-type>-adapter` (e.g., `sentinel-thermal-adapter`, `sentinel-modbus-adapter`).

## 2. Config Management

### Config file location

- All config files live in the project root `config/` directory
- Mounted read-only into containers at `/config/`
- Example: `./config/cameras.json:/config/cameras.json:ro`

### Config file naming

- Use lowercase kebab-case: `<source-type>.json` or `<source-type>-sources.json`
- Current files: `cameras.json`, `youtube-sources.json`

### Hot reload

Adapters should support hot reload via `POST /api/reload` so config changes can be applied without container restart. The adapter watches or re-reads its config file on this endpoint.

### Environment variables

- Service URLs: `<SERVICE_NAME>_URL` (e.g., `NOTIFIER_URL=http://notifier:8080`)
- Config paths: `<TYPE>_CONFIG_PATH` (e.g., `CAMERAS_CONFIG_PATH=/config/cameras.json`)
- RTMP destination: `STREAMING_RTMP_URL=rtmp://streaming:1935/live`
- MQTT broker: `MQTT_BROKER_URL=tcp://mosquitto:1883`
- Secrets/API keys: passed via `.env` file or Docker secrets, never committed

## 3. Monitoring

### Required health endpoint

Every service must expose `GET /healthz` returning JSON:

```json
{
  "status": "ok",
  "service": "<service-name>"
}
```

### Docker healthcheck template

All services must include a healthcheck in `docker-compose.yml`:

```yaml
healthcheck:
  test: ["CMD", "wget", "--spider", "-q", "http://localhost:8080/healthz"]
  interval: 30s
  timeout: 5s
  retries: 3
```

For services with longer startup times (streaming, web-frontend), add `start_period: 10s`.

### Stream health

Stream status (alive/dead) is determined **only by the streaming server**. Adapters do not report stream status. `web-backend` queries the streaming server for all stream information. This is a core design principle — see AGENTS.md Design Principle #4.

## 4. Resource Limit Guidelines

Resource limits prevent any single service from consuming all CPU/memory on the mini PC.

### CPU budgets

| Type | CPU Limit | Rationale |
|------|-----------|-----------|
| Re-encoding adapter (e.g., youtube-adapter) | 2.0 | libx264 encoding is CPU-intensive |
| Passthrough adapter (e.g., cctv-adapter) | 1.0 | `-c copy` uses minimal CPU |
| Non-FFmpeg services | No CPU limit needed | Lightweight HTTP services |

### Memory budgets

| Type | Memory Limit | Rationale |
|------|-------------|-----------|
| Re-encoding adapter | 512M | FFmpeg + encoding buffers |
| Passthrough adapter | 256M | FFmpeg passthrough + Go runtime |
| Streaming server | 256M | nginx-rtmp + HLS segment serving |
| Other services (web-backend, notifier, hw-gateway) | 128M | Lightweight HTTP/MQTT services |

### docker-compose.yml format

Set limits via `deploy.resources.limits`:

```yaml
services:
  youtube-adapter:
    deploy:
      resources:
        limits:
          cpus: "2.0"
          memory: 512M
```

## 5. Scaling Rules

### One adapter per source type

Each source type (RTSP, YouTube, thermal camera, etc.) gets its own adapter container. Do not mix source types in a single adapter.

### When to split an adapter

Split an adapter into multiple instances when it manages **20+ FFmpeg processes**. At that point, a single container becomes a reliability risk — one crash takes down all streams.

When splitting, use numbered suffixes: `sentinel-cctv-adapter-1`, `sentinel-cctv-adapter-2`, each with its own config file containing a subset of sources.

## References

- `AGENTS.md` — Design principles and architecture
- `docs/adapter-checklist.md` — Step-by-step guide for adding new adapters
- `services/streaming/AGENTS.md` — RTMP Input Specification
- `services/cctv-adapter/AGENTS.md` — Reference adapter implementation
