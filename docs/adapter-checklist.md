# Adapter Extension Checklist

Step-by-step guide for adding new adapters to Sentinel.

## Section 1: Stream Adapter (Video Source)

Stream adapters receive video from an external source and push it to the streaming server via RTMP.

### Prerequisites

- Read the **RTMP Input Specification** in `services/streaming/AGENTS.md`
- Read the reference implementation in `services/cctv-adapter/AGENTS.md`

### Step 1: Create the service directory

```
services/<source-type>-adapter/
├── Dockerfile
├── go.mod
├── main.go
└── AGENTS.md
```

### Step 2: Implement the adapter (Go skeleton)

**go.mod:**

```
module sentinel/<source-type>-adapter

go 1.22
```

**main.go skeleton:**

```go
package main

import (
    "encoding/json"
    "log"
    "net/http"
    "os"
)

func main() {
    // 1. Load source config from JSON file
    configPath := os.Getenv("<SOURCE_TYPE>_CONFIG_PATH")
    if configPath == "" {
        configPath = "/config/<source-type>.json"
    }

    streamURL := os.Getenv("STREAMING_RTMP_URL")
    if streamURL == "" {
        streamURL = "rtmp://streaming:1935/live"
    }

    // 2. Connect to source(s) — RTSP, file, URL, API, etc.
    // 3. For each source, launch FFmpeg:
    //      ffmpeg -i <source> ... -f flv rtmp://streaming:1935/live/{streamKey}
    //    See RTMP Input Spec for codec requirements:
    //      - H.264 Baseline/Main, NO B-frames (-tune zerolatency or -bf 0)
    //      - AAC audio
    //      - FLV container (-f flv)
    // 4. Handle reconnection with exponential backoff (1s → 30s max)
    // 5. Implement watchdog to kill hung FFmpeg processes (FFMPEG_TIMEOUT env var)

    // HTTP server for health and management
    mux := http.NewServeMux()

    mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
        w.Header().Set("Content-Type", "application/json")
        w.WriteHeader(http.StatusOK)
        json.NewEncoder(w).Encode(map[string]string{
            "status":  "ok",
            "service": "<source-type>-adapter",
        })
    })

    // Optional: status endpoint
    mux.HandleFunc("GET /api/status", func(w http.ResponseWriter, r *http.Request) {
        // Return per-source connection statuses
    })

    // Optional: reload endpoint
    mux.HandleFunc("POST /api/reload", func(w http.ResponseWriter, r *http.Request) {
        // Hot-reload config without restart
    })

    log.Println("<source-type>-adapter listening on :8080")
    log.Fatal(http.ListenAndServe(":8080", mux))
}
```

### Step 3: Create the Dockerfile

```dockerfile
FROM golang:1.22-alpine AS builder
WORKDIR /app
COPY go.mod ./
RUN go mod download
COPY *.go ./
RUN go build -o <source-type>-adapter .

FROM sentinel-ffmpeg-base
WORKDIR /app
COPY --from=builder /app/<source-type>-adapter .
RUN mkdir -p /config
EXPOSE 8080
CMD ["./<source-type>-adapter"]
```

**Note:** The base image must be pre-built before building the adapter:

```bash
docker build -f docker/ffmpeg-base.Dockerfile -t sentinel-ffmpeg-base .
```

If your source requires additional tools (e.g., `yt-dlp` for YouTube), add them in the runtime stage.

### Step 4: Add config file

Create `config/<source-type>.json` with your source definitions:

```json
[
  {
    "sourceId": "source-1",
    "name": "Source 1",
    "url": "..."
  }
]
```

### Step 5: Add docker-compose entry

Add to `docker-compose.yml`:

```yaml
  <source-type>-adapter:
    build: ./services/<source-type>-adapter
    container_name: sentinel-<source-type>-adapter
    restart: unless-stopped
    environment:
      - <SOURCE_TYPE>_CONFIG_PATH=/config/<source-type>.json
      - STREAMING_RTMP_URL=rtmp://streaming:1935/live
    healthcheck:
      test: ["CMD", "wget", "--spider", "-q", "http://localhost:8080/healthz"]
      interval: 30s
      timeout: 5s
      retries: 3
    volumes:
      - ./config/<source-type>.json:/config/<source-type>.json:ro
    depends_on:
      - streaming
    networks:
      - sentinel-net
    # ports: internal only
```

### Step 6: Create AGENTS.md

Create `services/<source-type>-adapter/AGENTS.md` documenting:
- Responsibility (what source type it handles)
- Inbound interfaces (source protocol)
- Outbound interfaces (RTMP to streaming)
- Environment variables
- Implementation notes

### Step 7: Test

1. `docker compose build <source-type>-adapter`
2. `docker compose up -d <source-type>-adapter`
3. Verify `/healthz` returns 200: `docker exec sentinel-<source-type>-adapter wget -qO- http://localhost:8080/healthz`
4. Verify stream appears in streaming server: `curl http://localhost:3080/api/streams` (via web-frontend proxy)
5. Verify HLS playback works in the web UI

### Key Rules

- **One adapter per source type** — don't mix RTSP and YouTube in one container
- **No status reporting** — the streaming server is the single source of truth for stream status
- **Conform to RTMP Input Spec** — H.264 no B-frames, AAC audio, FLV container
- **Expose `/healthz`** — required for Docker healthcheck
- **Use passthrough (`-c copy`) when possible** — only re-encode if the source is incompatible

---

## Section 2: H/W Adapter (Hardware Device)

H/W devices communicate with Sentinel through `hw-gateway` via MQTT. No new container is needed for most cases.

### Prerequisites

- Read the **MQTT Input Specification** in `services/hw-gateway/AGENTS.md`
- Read the full MQTT payload spec in `docs/api-mqtt.md`

### Case A: MQTT-native device (new device, existing signal types)

No code changes needed. The device just needs to:

1. Connect to the MQTT broker (`mosquitto:1883`)
2. Publish to existing topics with the correct payload format:
   - `safety/{siteId}/alert` — crisis signals (QoS 2)
   - `safety/{siteId}/heartbeat` — alive signals (QoS 0)
3. Subscribe to command topics if it supports remote control:
   - `safety/{siteId}/cmd/restart` (QoS 1)

**That's it.** No S/W changes required.

### Case B: New signal type (e.g., gas sensor, temperature alert)

When you need a new type of signal that doesn't fit existing MQTT topics:

1. **Define the new MQTT topic** following the naming pattern: `safety/{siteId}/<signal-type>`
2. **Add a handler in hw-gateway** (`services/hw-gateway/`):
   - Subscribe to the new topic
   - Parse the payload
   - Forward via HTTP to the appropriate S/W service (notifier, web-backend, etc.)
3. **Update `services/hw-gateway/AGENTS.md`** with the new topic in the MQTT Input Specification table
4. **Update `docs/api-mqtt.md`** with the full payload format
5. **Update the target service** if it needs a new endpoint to receive the signal

### Case C: Protocol adapter (non-MQTT device)

When the H/W device doesn't speak MQTT (e.g., Modbus, serial, proprietary TCP):

1. **Create a protocol adapter container**: `services/<protocol>-adapter/`
   - This container bridges the non-MQTT protocol to MQTT
   - It connects to the device using its native protocol
   - It publishes translated messages to the MQTT broker using standard topic formats
2. **Follow the same MQTT topic/payload spec** — hw-gateway doesn't need to change
3. **Add docker-compose entry** with access to the MQTT broker:
   ```yaml
     <protocol>-adapter:
       build: ./services/<protocol>-adapter
       container_name: sentinel-<protocol>-adapter
       restart: unless-stopped
       environment:
         - MQTT_BROKER_URL=tcp://mosquitto:1883
         - DEVICE_ADDRESS=<device-specific-config>
       healthcheck:
         test: ["CMD", "wget", "--spider", "-q", "http://localhost:8080/healthz"]
         interval: 30s
         timeout: 5s
         retries: 3
       depends_on:
         - mosquitto
       networks:
         - sentinel-net
   ```
4. **Create AGENTS.md** for the new adapter

---

## References

- `services/streaming/AGENTS.md` — RTMP Input Specification (codec requirements, endpoint format)
- `services/hw-gateway/AGENTS.md` — MQTT Input Specification (topic format, payload structure)
- `services/cctv-adapter/AGENTS.md` — Reference implementation for stream adapters
- `services/cctv-adapter/main.go` — Full working example with FFmpeg management, watchdog, reconnection
- `docs/api-mqtt.md` — Full MQTT payload specifications
