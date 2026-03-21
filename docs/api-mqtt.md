# MQTT Topic Specification — hw-gateway

Broker: `mqtt://mosquitto:1883` (internal via Docker network)

hw-gateway is the **sole MQTT client** on the S/W side. All other services communicate with hw-gateway via HTTP.

---

## Broker Configuration

| Parameter | Value |
|-----------|-------|
| Broker address | Configurable via `MQTT_BROKER_URL` env var (default: `mqtt://mosquitto:1883`) |
| Client ID | `sentinel-hw-gateway` |
| Clean session | `true` |
| Keep-alive | 60 seconds |
| Auto-reconnect | Yes, exponential backoff (1s → 2s → 4s → ... → 60s max) |

---

## Topics

### `safety/{siteId}/alert`

Crisis alert signal from H/W equipment.

| Property | Value |
|----------|-------|
| Direction | H/W → hw-gateway (subscribe) |
| QoS | 2 (exactly once) |
| Retain | `false` |
| Subscribe pattern | `safety/+/alert` |

**Payload (JSON):**

```json
{
  "deviceId": "string — unique device identifier",
  "siteId": "string — site identifier (matches topic)",
  "type": "string — alert type (e.g., 'scream', 'help', 'auto_stop', 'gas_leak')",
  "description": "string — human-readable description",
  "severity": "string — 'critical' | 'warning'",
  "timestamp": "string — ISO 8601 UTC (e.g., '2026-03-21T09:15:30Z')"
}
```

**Example:**

```json
{
  "deviceId": "PRESS-01",
  "siteId": "site1",
  "type": "scream",
  "description": "Scream detected near press machine #1",
  "severity": "critical",
  "timestamp": "2026-03-21T09:15:30Z"
}
```

**hw-gateway actions on receive:**

1. Parse and validate JSON payload
2. HTTP POST to **notifier** `http://notifier:8080/api/notify` with parsed payload
3. HTTP POST to **web-backend** `http://web-backend:8080/api/incidents` with parsed payload
4. Log message receipt and forwarding results

**Forwarded payload to notifier (`POST /api/notify`):**

```json
{
  "deviceId": "PRESS-01",
  "siteId": "site1",
  "type": "scream",
  "description": "Scream detected near press machine #1",
  "severity": "critical",
  "timestamp": "2026-03-21T09:15:30Z"
}
```

**Forwarded payload to web-backend (`POST /api/incidents`):**

```json
{
  "deviceId": "PRESS-01",
  "siteId": "site1",
  "type": "scream",
  "description": "Scream detected near press machine #1",
  "severity": "critical",
  "occurredAt": "2026-03-21T09:15:30Z"
}
```

---

### `safety/{siteId}/heartbeat`

Periodic alive signal from H/W equipment.

| Property | Value |
|----------|-------|
| Direction | H/W → hw-gateway (subscribe) |
| QoS | 0 (at most once) |
| Retain | `false` |
| Subscribe pattern | `safety/+/heartbeat` |
| Expected frequency | Every 10 seconds per device |

**Payload (JSON):**

```json
{
  "deviceId": "string — unique device identifier",
  "siteId": "string — site identifier (matches topic)",
  "status": "string — 'running' | 'stopped' | 'error'",
  "timestamp": "string — ISO 8601 UTC"
}
```

**Example:**

```json
{
  "deviceId": "PRESS-01",
  "siteId": "site1",
  "status": "running",
  "timestamp": "2026-03-21T09:15:40Z"
}
```

**hw-gateway actions on receive:**

1. Update in-memory device status (deviceId, siteId, alive=true, lastHeartbeat=timestamp)
2. If device was previously marked dead, log status change to alive
3. No HTTP forwarding (heartbeats are processed locally only)

**Timeout behavior:**

| Parameter | Value |
|-----------|-------|
| Dead threshold | Configurable via `HEARTBEAT_TIMEOUT` env var (default: 30 seconds) |
| Check interval | Every 10 seconds |
| On timeout | Mark device as `alive: false`, log warning |

---

### `safety/{siteId}/cmd/restart`

Remote restart command sent from S/W to H/W equipment.

| Property | Value |
|----------|-------|
| Direction | hw-gateway → H/W (publish) |
| QoS | 1 (at least once) |
| Retain | `false` |
| Publish pattern | `safety/{siteId}/cmd/restart` (specific siteId, not wildcard) |

**Payload (JSON):**

```json
{
  "deviceId": "string — target device identifier",
  "siteId": "string — site identifier",
  "requestedBy": "string — username who initiated the restart",
  "reason": "string (optional) — reason for restart",
  "timestamp": "string — ISO 8601 UTC"
}
```

**Example:**

```json
{
  "deviceId": "PRESS-01",
  "siteId": "site1",
  "requestedBy": "admin",
  "reason": "Crisis resolved, resuming production",
  "timestamp": "2026-03-21T09:30:00Z"
}
```

**Trigger:** HTTP POST from web-backend to hw-gateway `POST /api/restart`

**Request to hw-gateway (`POST /api/restart`):**

```json
{
  "siteId": "site1",
  "deviceId": "PRESS-01",
  "requestedBy": "admin",
  "reason": "Crisis resolved, resuming production"
}
```

**Response from hw-gateway:**

| Status | Description | Body |
|--------|-------------|------|
| 200 | Command published | `{"status": "sent", "topic": "safety/site1/cmd/restart"}` |
| 400 | Missing required fields | `{"error": "siteId and deviceId are required"}` |
| 503 | MQTT broker disconnected | `{"error": "MQTT broker not connected"}` |

---

## QoS Level Rationale

| Topic | QoS | Rationale |
|-------|-----|-----------|
| `safety/+/alert` | 2 | Crisis alerts must not be lost or duplicated — each represents a safety event |
| `safety/+/heartbeat` | 0 | High frequency, losing one heartbeat is acceptable (timeout-based detection) |
| `safety/{siteId}/cmd/restart` | 1 | Restart commands should be delivered but duplicate delivery is safe (idempotent) |

---

## Topic Naming Convention

```
safety/{siteId}/{messageType}
safety/{siteId}/cmd/{commandName}
```

- `{siteId}`: Alphanumeric site identifier (e.g., `site1`, `factory-a`)
- Subscribe topics use `+` single-level wildcard for siteId
- Command topics use specific siteId (no wildcard)

---

## Error Handling

| Scenario | hw-gateway Behavior |
|----------|-------------------|
| Malformed JSON payload | Log error, skip processing, do not forward |
| Missing required fields | Log warning with received payload, skip processing |
| HTTP forwarding failure (notifier) | Log error, continue with web-backend forwarding |
| HTTP forwarding failure (web-backend) | Log error, retry once after 1 second |
| MQTT broker disconnect | Auto-reconnect with exponential backoff, log each attempt |
| MQTT broker unreachable on startup | Retry connection indefinitely, log every 30 seconds |
