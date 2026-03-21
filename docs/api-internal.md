# Inter-Service HTTP API Specification

Internal service-to-service communication within the `sentinel-net` Docker bridge network. These endpoints are **not** exposed externally — security relies on network isolation.

All payloads are JSON (`Content-Type: application/json`). Timestamps use ISO 8601 UTC format.

---

## Service Communication Map

```
hw-gateway  ──POST /api/notify──────→  notifier
hw-gateway  ──POST /api/incidents───→  web-backend

notifier    ──GET  /api/contacts────→  web-backend
notifier    ──POST /api/links/temp──→  web-backend
notifier    ──POST /api/alarms──────→  web-backend

web-backend ──POST /api/restart─────→  hw-gateway
web-backend ──GET  /api/streams─────→  streaming
web-backend ──GET  /api/cameras/status→ cctv-adapter
```

---

## hw-gateway → notifier

### POST http://notifier:8080/api/notify

Forward crisis alert for KakaoTalk/SMS notification dispatch.

**Trigger:** MQTT message received on `safety/+/alert` topic.

**Request body:**
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

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| deviceId | string | Yes | Unique device identifier |
| siteId | string | Yes | Site identifier (from MQTT topic) |
| type | string | Yes | Alert type: `scream`, `help`, `auto_stop`, `gas_leak` |
| description | string | Yes | Human-readable crisis description |
| severity | string | Yes | `critical` or `warning` |
| timestamp | string | Yes | ISO 8601 UTC when crisis occurred at H/W |

**Responses:**

| Status | Description | Body |
|--------|-------------|------|
| 200 | Notification dispatch initiated | `{"status": "accepted", "contactCount": 3}` |
| 400 | Invalid payload | `{"error": "siteId is required"}` |
| 500 | Internal error | `{"error": "internal server error"}` |

**Notes:**
- Notifier processes notifications **asynchronously** — 200 means accepted, not delivered.
- On receive, notifier: (1) fetches contacts from web-backend, (2) requests temp link from web-backend, (3) sends KakaoTalk/SMS to each contact.

---

## hw-gateway → web-backend

### POST http://web-backend:8080/api/incidents

Create incident record and trigger WebSocket broadcast.

**Trigger:** MQTT message received on `safety/+/alert` topic (sent in parallel with notifier call).

**Request body:**
```json
{
  "siteId": "site1",
  "description": "Scream detected near press machine #1",
  "occurredAt": "2026-03-21T09:15:30Z"
}
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| siteId | string | Yes | Site identifier |
| description | string | No | Human-readable crisis description |
| occurredAt | string | Yes | ISO 8601 UTC when crisis occurred |

**Responses:**

| Status | Description | Body |
|--------|-------------|------|
| 201 | Incident created | `{"id": 1, "siteId": "site1", "occurredAt": "2026-03-21T09:15:30Z", "confirmedAt": null, "confirmedBy": null}` |
| 400 | Validation error | `{"error": "siteId is required"}` |

**Side effect:** Broadcasts `crisis_alert` WebSocket event to all connected clients.

**Error handling by hw-gateway:**
- On failure, retry once after 1 second.
- If retry fails, log error (alert is still delivered via notifier path).

---

## notifier → web-backend

### GET http://web-backend:8080/api/contacts

Fetch all notification target contacts for crisis alert dispatch.

**Trigger:** Called by notifier upon receiving a crisis alert from hw-gateway.

**Request:** No query params or body.

**Response (200):**
```json
[
  {"id": 1, "name": "Kim Cheolsu", "phone": "010-1234-5678"},
  {"id": 2, "name": "Park Jimin", "phone": "010-9876-5432"}
]
```

| Field | Type | Description |
|-------|------|-------------|
| id | integer | Contact ID |
| name | string | Contact name |
| phone | string | Phone number (format: `010-XXXX-XXXX`) |

**Notes:**
- Returns all contacts (no pagination for internal use).
- Empty array `[]` if no contacts configured — notifier logs warning, no notifications sent.

---

### POST http://web-backend:8080/api/links/temp

Request a temporary CCTV viewing link to include in notification messages.

**Trigger:** Called by notifier during crisis notification dispatch, before sending KakaoTalk/SMS.

**Request body:**
```json
{
  "label": "Crisis alert 2026-03-21T09:15:30Z"
}
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| label | string | No | Descriptive label for admin reference |

**Response (201):**
```json
{
  "id": "550e8400-e29b-41d4-a716-446655440000",
  "token": "eyJhbGciOiJIUzI1NiIs...",
  "url": "https://sentinel.example.com/view/eyJhbGciOiJIUzI1NiIs...",
  "expiresAt": "2026-03-22T09:15:30Z"
}
```

| Field | Type | Description |
|-------|------|-------------|
| id | string (UUID) | Unique link identifier |
| token | string | JWT token embedded in URL |
| url | string | Full URL for the temporary viewer page |
| expiresAt | string | ISO 8601 expiry (24 hours from creation) |

**Error handling by notifier:**
- If this call fails, notifier still sends KakaoTalk/SMS **without** the CCTV link (degraded mode).
- Logs warning about missing link.

---

### POST http://web-backend:8080/api/alarms

Create a system alarm when all notification channels fail for a contact.

**Trigger:** Called by notifier when both KakaoTalk and SMS fail for a contact.

**Request body:**
```json
{
  "type": "notification_failure",
  "message": "Failed to deliver crisis alert to Kim Cheolsu (KakaoTalk + SMS both failed)",
  "details": {
    "contactId": 1,
    "contactName": "Kim Cheolsu",
    "incidentId": 42,
    "kakaoError": "API timeout",
    "smsError": "Invalid phone number"
  }
}
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| type | string | Yes | Alarm type (e.g., `notification_failure`) |
| message | string | Yes | Human-readable alarm description |
| details | object | No | Additional context (varies by alarm type) |

**Response (201):**
```json
{
  "id": 1,
  "type": "notification_failure",
  "message": "Failed to deliver crisis alert to Kim Cheolsu (KakaoTalk + SMS both failed)",
  "createdAt": "2026-03-21T09:15:35Z"
}
```

**Side effect:** Broadcasts `system_alarm` WebSocket event to admin clients only.

---

## web-backend → hw-gateway

### POST http://hw-gateway:8080/api/restart

Forward equipment restart command for MQTT publish.

**Trigger:** User clicks restart button in web UI → `POST /api/equipment/restart` on web-backend → forwarded here.

**Request body:**
```json
{
  "siteId": "site1",
  "deviceId": "PRESS-01",
  "requestedBy": "admin",
  "reason": "Crisis resolved, resuming production"
}
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| siteId | string | Yes | Target site identifier |
| deviceId | string | Yes | Target device identifier |
| requestedBy | string | Yes | Username who initiated the restart |
| reason | string | No | Human-readable reason for restart |

**Responses:**

| Status | Description | Body |
|--------|-------------|------|
| 200 | Command published to MQTT | `{"status": "sent", "topic": "safety/site1/cmd/restart"}` |
| 400 | Missing required fields | `{"error": "siteId and deviceId are required"}` |
| 503 | MQTT broker disconnected | `{"error": "MQTT broker not connected"}` |

**Notes:**
- hw-gateway publishes to MQTT topic `safety/{siteId}/cmd/restart` with QoS 1.
- web-backend forwards the hw-gateway response directly to the web client.

---

## web-backend → streaming

### GET http://streaming:8080/api/streams

Fetch list of available HLS stream URLs.

**Trigger:** Called by web-backend when serving `GET /api/cameras` to enrich camera data with live stream URLs.

**Request:** No query params or body.

**Response (200):**
```json
[
  {
    "cameraId": "cam1",
    "hlsUrl": "/live/cam1/index.m3u8",
    "active": true,
    "startedAt": "2026-03-21T08:00:00Z"
  },
  {
    "cameraId": "cam2",
    "hlsUrl": "/live/cam2/index.m3u8",
    "active": true,
    "startedAt": "2026-03-21T08:00:05Z"
  }
]
```

| Field | Type | Description |
|-------|------|-------------|
| cameraId | string | Camera identifier (matches config) |
| hlsUrl | string | Relative HLS playlist URL path |
| active | boolean | Whether the stream is currently being served |
| startedAt | string | ISO 8601 when the stream started |

**Notes:**
- web-backend caches this response with a short TTL (e.g., 10 seconds) to avoid excessive internal calls.
- `hlsUrl` is relative — web-backend constructs the full URL as `http://streaming:8080{hlsUrl}` for internal use or proxied URL for clients.
- Empty array `[]` if no streams are active.

---

## web-backend → cctv-adapter

### GET http://cctv-adapter:8080/api/cameras/status

Fetch per-camera connection health status.

**Trigger:** Called by web-backend when serving `GET /api/cameras` to include camera connection status.

**Request:** No query params or body.

**Response (200):**
```json
[
  {
    "cameraId": "cam1",
    "status": "connected",
    "connectedAt": "2026-03-21T08:00:00Z",
    "lastError": null
  },
  {
    "cameraId": "cam2",
    "status": "reconnecting",
    "connectedAt": null,
    "lastError": "RTSP connection timeout"
  }
]
```

| Field | Type | Description |
|-------|------|-------------|
| cameraId | string | Camera identifier (matches config) |
| status | string | `connected`, `disconnected`, or `reconnecting` |
| connectedAt | string/null | ISO 8601 when last successfully connected |
| lastError | string/null | Last error message if not connected |

**Notes:**
- web-backend merges this data with DB camera records and streaming URLs to build the full `GET /api/cameras` response.
- Empty array `[]` if no cameras configured.

---

## hw-gateway → web-backend (equipment status)

### GET http://hw-gateway:8080/api/equipment/status

Fetch all device statuses tracked by heartbeat monitoring.

**Trigger:** Called by web-backend on demand (e.g., when broadcasting `equipment_status` WebSocket events or serving admin status views).

**Request:** No query params or body.

**Response (200):**
```json
[
  {
    "deviceId": "PRESS-01",
    "siteId": "site1",
    "alive": true,
    "lastHeartbeat": "2026-03-21T09:15:40Z"
  },
  {
    "deviceId": "PRESS-02",
    "siteId": "site1",
    "alive": false,
    "lastHeartbeat": "2026-03-21T09:14:50Z"
  }
]
```

| Field | Type | Description |
|-------|------|-------------|
| deviceId | string | Unique device identifier |
| siteId | string | Site the device belongs to |
| alive | boolean | `true` if heartbeat received within timeout window |
| lastHeartbeat | string | ISO 8601 of last received heartbeat |

**Notes:**
- Data is held in hw-gateway memory only (not persisted to DB).
- Default heartbeat timeout: 30 seconds (configurable via `HEARTBEAT_TIMEOUT` env var).
- Empty array `[]` if no heartbeats have been received yet.

---

## Error Handling Conventions

All internal endpoints follow these conventions:

| Status | Meaning |
|--------|---------|
| 200 | Success |
| 201 | Resource created |
| 400 | Invalid request (missing/malformed fields) |
| 500 | Internal server error |
| 503 | Upstream dependency unavailable |

**Error body format:**
```json
{
  "error": "human-readable error message"
}
```

**Retry policy by caller:**

| Caller | Target | On Failure |
|--------|--------|------------|
| hw-gateway | notifier | Log error, continue (do not block incident creation) |
| hw-gateway | web-backend | Retry once after 1s, then log error |
| notifier | web-backend (contacts) | Log error, abort notification for this event |
| notifier | web-backend (temp link) | Log warning, send notification without link |
| notifier | web-backend (alarms) | Log error, no retry (alarm is non-critical) |
| web-backend | hw-gateway | Return 502 to client |
| web-backend | streaming | Return cached data or empty list |
| web-backend | cctv-adapter | Return `disconnected` status for all cameras |

---

## Timeout Configuration

All internal HTTP calls should use these timeouts:

| Parameter | Value |
|-----------|-------|
| Connection timeout | 3 seconds |
| Read timeout | 5 seconds |
| Total request timeout | 10 seconds |

Services that are unreachable at startup should be retried — callers must handle temporary unavailability during container startup ordering.
