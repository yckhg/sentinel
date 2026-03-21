# WebSocket Event Specification — web-backend

Endpoint: `ws://web-backend:8080/ws` (internal) / `wss://<domain>/ws` (external via nginx proxy)

---

## Connection Lifecycle

### 1. Connect

Open a WebSocket connection with JWT authentication via query parameter:

```
ws://web-backend:8080/ws?token=<JWT>
```

**Authentication:**
- JWT token is validated on connection handshake
- Invalid or expired token → connection rejected with HTTP 401
- Temporary viewer tokens (from `/api/links/temp`) are also accepted — viewer receives `crisis_alert` events only

**Success response (text frame):**
```json
{
  "type": "connected",
  "payload": {
    "userId": 1,
    "role": "admin",
    "connectedAt": "2026-03-21T09:00:00Z"
  }
}
```

**Failure:** HTTP 401 Unauthorized (connection not upgraded)

---

### 2. Heartbeat (Keep-Alive)

Server sends periodic ping frames to detect stale connections. Clients must respond with pong (handled automatically by most WebSocket libraries).

| Parameter | Value |
|-----------|-------|
| Ping interval | 30 seconds |
| Pong timeout | 10 seconds |
| Max missed pongs | 1 (connection closed after) |

If the server does not receive a pong within the timeout, the connection is closed with code `1001 (Going Away)`.

---

### 3. Reconnect

Clients should implement automatic reconnection with exponential backoff:

| Attempt | Delay |
|---------|-------|
| 1 | 1 second |
| 2 | 2 seconds |
| 3 | 4 seconds |
| 4+ | 8 seconds (max) |

On reconnect, the client must re-authenticate with a valid JWT. If the token has expired, the client should obtain a new token via `/auth/login` before reconnecting.

---

### 4. Disconnect

**Server-initiated close codes:**

| Code | Reason | Action |
|------|--------|--------|
| 1000 | Normal closure | Server shutting down gracefully |
| 1001 | Going away | Heartbeat timeout |
| 1008 | Policy violation | Token expired mid-session |
| 4001 | Authentication failed | Invalid token on connect |

**Client-initiated:** Standard close frame (`1000`).

---

## Message Format

All messages are JSON text frames with the following envelope:

```json
{
  "type": "string",
  "payload": { ... },
  "timestamp": "ISO 8601 string"
}
```

| Field | Type | Description |
|-------|------|-------------|
| type | string | Event type identifier |
| payload | object | Event-specific data |
| timestamp | string | ISO 8601 UTC timestamp of the event |

---

## Event Types

### crisis_alert

Broadcast to **all connected clients** (including temporary viewers) when a crisis incident is received from hw-gateway via `POST /api/incidents`.

**Direction:** Server → Client

**Trigger:** `POST /api/incidents` creates a new incident record and broadcasts this event.

**Payload:**
```json
{
  "type": "crisis_alert",
  "payload": {
    "incidentId": 42,
    "siteId": "site1",
    "description": "Scream detected in Factory 1 press area",
    "occurredAt": "2026-03-21T09:15:30Z",
    "site": {
      "address": "123 Industrial Blvd, Ansan",
      "managerName": "Kim Cheolsu",
      "managerPhone": "010-1234-5678"
    }
  },
  "timestamp": "2026-03-21T09:15:31Z"
}
```

**Payload fields:**

| Field | Type | Description |
|-------|------|-------------|
| incidentId | integer | Auto-increment ID of the created incident |
| siteId | string | Site identifier from the MQTT topic |
| description | string | Human-readable crisis description |
| occurredAt | string | ISO 8601 timestamp when the crisis occurred at H/W |
| site.address | string | Site address for emergency reference |
| site.managerName | string | Site manager name |
| site.managerPhone | string | Site manager phone number |

---

### equipment_status

Broadcast to **all authenticated clients** (excludes temporary viewers) when a device's alive/dead status changes based on heartbeat monitoring.

**Direction:** Server → Client

**Trigger:** hw-gateway heartbeat monitoring detects a status change (alive → dead or dead → alive).

**Payload:**
```json
{
  "type": "equipment_status",
  "payload": {
    "deviceId": "press-01",
    "siteId": "site1",
    "alive": false,
    "lastHeartbeat": "2026-03-21T09:14:50Z",
    "changedAt": "2026-03-21T09:15:20Z"
  },
  "timestamp": "2026-03-21T09:15:20Z"
}
```

**Payload fields:**

| Field | Type | Description |
|-------|------|-------------|
| deviceId | string | Unique device identifier |
| siteId | string | Site the device belongs to |
| alive | boolean | `true` if device is responding, `false` if heartbeat timed out |
| lastHeartbeat | string | ISO 8601 timestamp of the last received heartbeat |
| changedAt | string | ISO 8601 timestamp when the status transition occurred |

**Notes:**
- Only sent on **status change**, not on every heartbeat
- Default heartbeat timeout: 30 seconds (configurable)
- web-backend receives status updates from hw-gateway via `GET /api/equipment/status` polling or direct push

---

### system_alarm

Broadcast to **admin clients only** when a system-level alarm occurs (e.g., notification delivery failure).

**Direction:** Server → Client

**Trigger:** `POST /api/alarms` creates a system alarm record and broadcasts to admin WebSocket connections.

**Payload:**
```json
{
  "type": "system_alarm",
  "payload": {
    "alarmId": 7,
    "severity": "high",
    "source": "notifier",
    "message": "Failed to deliver crisis alert to contact Kim Cheolsu (KakaoTalk + SMS both failed)",
    "details": {
      "contactId": 3,
      "contactName": "Kim Cheolsu",
      "incidentId": 42,
      "kakaoError": "API timeout",
      "smsError": "Invalid phone number"
    },
    "createdAt": "2026-03-21T09:15:35Z"
  },
  "timestamp": "2026-03-21T09:15:35Z"
}
```

**Payload fields:**

| Field | Type | Description |
|-------|------|-------------|
| alarmId | integer | Auto-increment alarm ID |
| severity | string | `"low"`, `"medium"`, `"high"`, `"critical"` |
| source | string | Service that generated the alarm |
| message | string | Human-readable alarm description |
| details | object | Alarm-specific context (varies by source) |
| createdAt | string | ISO 8601 timestamp when the alarm was created |

---

## Event Delivery by Role

| Event | Admin | User | Temp Viewer |
|-------|-------|------|-------------|
| crisis_alert | Yes | Yes | Yes |
| equipment_status | Yes | Yes | No |
| system_alarm | Yes | No | No |

---

## Client Implementation Notes

### Connecting (JavaScript example)

```javascript
const token = localStorage.getItem("jwt");
const ws = new WebSocket(`wss://example.com/ws?token=${token}`);

ws.onopen = () => {
  console.log("Connected to WebSocket");
};

ws.onmessage = (event) => {
  const msg = JSON.parse(event.data);
  switch (msg.type) {
    case "connected":
      console.log("Authenticated as", msg.payload.role);
      break;
    case "crisis_alert":
      showCrisisAlert(msg.payload);
      break;
    case "equipment_status":
      updateEquipmentStatus(msg.payload);
      break;
    case "system_alarm":
      showSystemAlarm(msg.payload);
      break;
  }
};

ws.onclose = (event) => {
  if (event.code === 1008) {
    // Token expired, re-login needed
    redirectToLogin();
  } else {
    // Reconnect with backoff
    scheduleReconnect();
  }
};
```

### Error Handling

- **Token expired mid-session:** Server closes with code `1008`. Client should re-authenticate.
- **Network disruption:** Client detects via `onclose` event. Implement reconnection with backoff.
- **Message parsing failure:** Log and ignore malformed messages. Do not disconnect.
