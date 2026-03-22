# AGENTS.md — hw-gateway

## Responsibility

The sole S/W contact point with H/W. Receives signals from H/W via MQTT, translates into HTTP calls to S/W services. Also receives commands from S/W and publishes to H/W via MQTT.

## MQTT Input Specification

Any H/W device that connects to this system MUST conform to this spec. Full payload details in `docs/api-mqtt.md`.

| Topic | Direction | QoS | Payload |
|-------|-----------|-----|---------|
| `safety/{siteId}/alert` | H/W → gateway (subscribe) | 2 (exactly once) | `{deviceId, siteId, type, description, severity, timestamp}` |
| `safety/{siteId}/heartbeat` | H/W → gateway (subscribe) | 0 (at most once) | `{deviceId, siteId, timestamp}` |
| `safety/{siteId}/cmd/restart` | gateway → H/W (publish) | 1 (at least once) | `{deviceId, siteId, reason, timestamp}` |

**To connect a new H/W device**: implement these MQTT topics with the payload format above, publish to the same broker. No S/W changes needed.

**To add a new signal type**: add a new MQTT topic to this spec, add a handler in hw-gateway, add HTTP forwarding to the appropriate S/W service. See [Adapter Checklist](../../docs/adapter-checklist.md) → Section 2 for H/W extension cases.

## HTTP Output Specification

hw-gateway translates MQTT signals into HTTP calls to S/W services:

| Event | Target | Method | Purpose |
|-------|--------|--------|---------|
| Crisis alert | notifier | POST `/api/notify` | Trigger notification chain |
| Crisis alert | web-backend | POST `/api/incidents` | Record incident + WebSocket push |
| Heartbeat | (internal) | — | Update in-memory equipment status |

## HTTP Input (from S/W)

| Endpoint | Method | Description |
|----------|--------|-------------|
| POST `/api/restart` | HTTP | Receive restart command from web-backend, publish to MQTT |
| GET `/api/equipment/status` | HTTP | Return equipment alive/dead status |
| GET `/healthz` | HTTP | Health check |

## Equipment Status

In-memory only (no DB). Tracks per device:
- `deviceId`, `siteId`, `alive` (bool), `lastHeartbeat` (timestamp)
- Device marked "dead" if no heartbeat within `HEARTBEAT_TIMEOUT` (default 30s)

## Implementation Notes

- MQTT auto-reconnect with exponential backoff on broker disconnect
- Crisis forwarding to notifier and web-backend is parallel (goroutines with context timeout)
- All HTTP calls have 10s context timeout
- Retry with exponential backoff + jitter on forwarding failure
- Broker address: `MQTT_BROKER_URL` env var (default `tcp://mosquitto:1883`)
