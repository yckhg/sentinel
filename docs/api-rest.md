# REST API Specification — web-backend

Base URL: `http://web-backend:8080` (internal) / `https://<domain>/api` (external via nginx proxy)

All `/api/*` endpoints require JWT authentication via `Authorization: Bearer <token>` header unless otherwise noted.

---

## Authentication

### POST /auth/register

Create a new user account (pending admin approval).

**Auth required:** No

**Request body:**
```json
{
  "username": "string (required, unique)",
  "password": "string (required, min 8 chars)",
  "name": "string (required)"
}
```

**Responses:**

| Status | Description | Body |
|--------|-------------|------|
| 201 | Created (pending approval) | `{"id": 1, "username": "john", "name": "John", "status": "pending"}` |
| 400 | Validation error | `{"error": "password must be at least 8 characters"}` |
| 409 | Username already exists | `{"error": "username already exists"}` |

---

### POST /auth/login

Authenticate and receive JWT token.

**Auth required:** No

**Request body:**
```json
{
  "username": "string (required)",
  "password": "string (required)"
}
```

**Responses:**

| Status | Description | Body |
|--------|-------------|------|
| 200 | Login success | `{"token": "eyJ...", "user": {"id": 1, "username": "john", "role": "user"}}` |
| 401 | Invalid credentials | `{"error": "invalid username or password"}` |
| 403 | Account not approved | `{"error": "account pending approval"}` |

**JWT payload:**
```json
{
  "userId": 1,
  "role": "admin|user",
  "exp": 1700000000
}
```

---

### GET /auth/pending

List users awaiting approval.

**Auth required:** Yes (admin only)

**Responses:**

| Status | Description | Body |
|--------|-------------|------|
| 200 | Pending users | `[{"id": 2, "username": "jane", "name": "Jane", "status": "pending", "createdAt": "2026-01-01T00:00:00Z"}]` |
| 403 | Not admin | `{"error": "admin access required"}` |

---

### POST /auth/approve/{userId}

Approve a pending user registration.

**Auth required:** Yes (admin only)

**Path params:** `userId` — integer

**Responses:**

| Status | Description | Body |
|--------|-------------|------|
| 200 | Approved | `{"id": 2, "status": "active"}` |
| 403 | Not admin | `{"error": "admin access required"}` |
| 404 | User not found | `{"error": "user not found"}` |

---

### POST /auth/reject/{userId}

Reject a pending user registration.

**Auth required:** Yes (admin only)

**Path params:** `userId` — integer

**Responses:**

| Status | Description | Body |
|--------|-------------|------|
| 200 | Rejected | `{"id": 2, "status": "rejected"}` |
| 403 | Not admin | `{"error": "admin access required"}` |
| 404 | User not found | `{"error": "user not found"}` |

---

## Contacts

### GET /api/contacts

List all notification target contacts.

**Auth required:** Yes

**Responses:**

| Status | Description | Body |
|--------|-------------|------|
| 200 | Contact list | `[{"id": 1, "name": "Kim", "phone": "010-1234-5678"}]` |

---

### POST /api/contacts

Create a new contact.

**Auth required:** Yes (admin)

**Request body:**
```json
{
  "name": "string (required)",
  "phone": "string (required, format: 010-XXXX-XXXX)"
}
```

**Responses:**

| Status | Description | Body |
|--------|-------------|------|
| 201 | Created | `{"id": 2, "name": "Lee", "phone": "010-9876-5432"}` |
| 400 | Validation error | `{"error": "invalid phone number format"}` |

---

### PUT /api/contacts/{id}

Update an existing contact.

**Auth required:** Yes (admin)

**Path params:** `id` — integer

**Request body:**
```json
{
  "name": "string (optional)",
  "phone": "string (optional, format: 010-XXXX-XXXX)"
}
```

**Responses:**

| Status | Description | Body |
|--------|-------------|------|
| 200 | Updated | `{"id": 2, "name": "Lee Updated", "phone": "010-9876-5432"}` |
| 400 | Validation error | `{"error": "invalid phone number format"}` |
| 404 | Not found | `{"error": "contact not found"}` |

---

### DELETE /api/contacts/{id}

Delete a contact.

**Auth required:** Yes (admin)

**Path params:** `id` — integer

**Responses:**

| Status | Description | Body |
|--------|-------------|------|
| 204 | Deleted | (no body) |
| 404 | Not found | `{"error": "contact not found"}` |

---

## Sites

### GET /api/sites

Get site information.

**Auth required:** Yes

**Responses:**

| Status | Description | Body |
|--------|-------------|------|
| 200 | Site info | `[{"id": 1, "address": "Seoul Guro-gu ...", "managerName": "Park", "managerPhone": "010-1111-2222"}]` |

---

### PUT /api/sites/{id}

Update site information.

**Auth required:** Yes (admin)

**Path params:** `id` — integer

**Request body:**
```json
{
  "address": "string (optional)",
  "managerName": "string (optional)",
  "managerPhone": "string (optional)"
}
```

**Responses:**

| Status | Description | Body |
|--------|-------------|------|
| 200 | Updated | `{"id": 1, "address": "Seoul Guro-gu ...", "managerName": "Park", "managerPhone": "010-1111-2222"}` |
| 404 | Not found | `{"error": "site not found"}` |

---

## Cameras

### GET /api/cameras

List all cameras with their HLS stream URLs and status.

**Auth required:** Yes

**Responses:**

| Status | Description | Body |
|--------|-------------|------|
| 200 | Camera list | See example below |

**Response example:**
```json
[
  {
    "id": 1,
    "name": "Press Area Cam 1",
    "location": "Factory 1",
    "zone": "Factory 1 press area",
    "hlsUrl": "http://streaming:8080/live/cam1/index.m3u8",
    "status": "connected"
  }
]
```

`status` values: `connected`, `disconnected`

---

## Incidents

### POST /api/incidents

Create a new crisis incident record (called by hw-gateway).

**Auth required:** No (internal service call; validated by network isolation)

**Request body:**
```json
{
  "siteId": "string (required)",
  "description": "string (optional)",
  "occurredAt": "ISO 8601 datetime (required)"
}
```

**Responses:**

| Status | Description | Body |
|--------|-------------|------|
| 201 | Created | `{"id": 1, "siteId": "site1", "occurredAt": "2026-01-01T12:00:00Z", "confirmedAt": null, "confirmedBy": null}` |
| 400 | Validation error | `{"error": "siteId is required"}` |

**Side effect:** Broadcasts `crisis_alert` event to all connected WebSocket clients.

---

### GET /api/incidents

List past incidents with pagination and date filtering.

**Auth required:** Yes

**Query params:**

| Param | Type | Default | Description |
|-------|------|---------|-------------|
| page | integer | 1 | Page number |
| limit | integer | 20 | Items per page (max 100) |
| from | ISO 8601 date | — | Filter: occurred after this date |
| to | ISO 8601 date | — | Filter: occurred before this date |

**Responses:**

| Status | Description | Body |
|--------|-------------|------|
| 200 | Incident list | See example below |

**Response example:**
```json
{
  "data": [
    {
      "id": 1,
      "siteId": "site1",
      "description": "Scream detected in press area",
      "occurredAt": "2026-01-01T12:00:00Z",
      "confirmedAt": "2026-01-01T12:05:00Z",
      "confirmedBy": "admin"
    }
  ],
  "pagination": {
    "page": 1,
    "limit": 20,
    "total": 42
  }
}
```

Results are sorted by `occurredAt` descending (newest first).

---

## Temporary Links

### POST /api/links/temp

Generate a temporary CCTV viewing link (JWT-based, 24h expiry).

**Auth required:** Yes (admin) or internal service call from notifier

**Request body:**
```json
{
  "label": "string (optional, descriptive label for admin reference)"
}
```

**Responses:**

| Status | Description | Body |
|--------|-------------|------|
| 201 | Link created | `{"id": "uuid", "token": "eyJ...", "url": "https://<domain>/view/eyJ...", "expiresAt": "2026-01-02T12:00:00Z"}` |

---

### GET /api/links/verify/{token}

Validate a temporary link token and return access info.

**Auth required:** No (token is the auth mechanism)

**Path params:** `token` — JWT string

**Responses:**

| Status | Description | Body |
|--------|-------------|------|
| 200 | Valid token | `{"valid": true, "expiresAt": "2026-01-02T12:00:00Z"}` |
| 401 | Invalid/expired/revoked | `{"error": "token expired or revoked"}` |

---

### GET /api/links

List all active temporary links.

**Auth required:** Yes (admin only)

**Responses:**

| Status | Description | Body |
|--------|-------------|------|
| 200 | Active links | `[{"id": "uuid", "label": "For inspector", "createdAt": "2026-01-01T12:00:00Z", "expiresAt": "2026-01-02T12:00:00Z"}]` |

---

### DELETE /api/links/{id}

Revoke a temporary link (adds token to in-memory blacklist).

**Auth required:** Yes (admin only)

**Path params:** `id` — UUID string

**Responses:**

| Status | Description | Body |
|--------|-------------|------|
| 204 | Revoked | (no body) |
| 404 | Not found | `{"error": "link not found"}` |

---

## Equipment

### POST /api/equipment/restart

Send a restart command to equipment via hw-gateway.

**Auth required:** Yes (admin or authorized user)

**Request body:**
```json
{
  "siteId": "string (required)",
  "deviceId": "string (required)",
  "reason": "string (optional)"
}
```

**Responses:**

| Status | Description | Body |
|--------|-------------|------|
| 200 | Restart sent | `{"success": true, "message": "restart command sent"}` |
| 502 | hw-gateway error | `{"error": "failed to reach hw-gateway"}` |

**Side effect:** Logs restart request with requesting user info.

---

## Alarms

### POST /api/alarms

Create a system alarm (called by notifier when all notification channels fail).

**Auth required:** No (internal service call)

**Request body:**
```json
{
  "type": "string (required, e.g., 'notification_failure')",
  "message": "string (required)",
  "details": "object (optional, additional context)"
}
```

**Responses:**

| Status | Description | Body |
|--------|-------------|------|
| 201 | Alarm created | `{"id": 1, "type": "notification_failure", "message": "...", "createdAt": "2026-01-01T12:00:00Z"}` |

**Side effect:** Broadcasts `system_alarm` event to all connected WebSocket clients.

---

### GET /api/alarms

List recent system alarms.

**Auth required:** Yes

**Query params:**

| Param | Type | Default | Description |
|-------|------|---------|-------------|
| page | integer | 1 | Page number |
| limit | integer | 20 | Items per page |

**Responses:**

| Status | Description | Body |
|--------|-------------|------|
| 200 | Alarm list | `{"data": [{"id": 1, "type": "notification_failure", "message": "...", "createdAt": "..."}], "pagination": {"page": 1, "limit": 20, "total": 5}}` |

---

## Common Error Responses

All endpoints may return these errors:

| Status | Description | Body |
|--------|-------------|------|
| 401 | Missing or invalid JWT | `{"error": "unauthorized"}` |
| 403 | Insufficient permissions | `{"error": "admin access required"}` |
| 500 | Internal server error | `{"error": "internal server error"}` |

## Conventions

- All request/response bodies are JSON (`Content-Type: application/json`)
- Timestamps use ISO 8601 format (`2026-01-01T12:00:00Z`)
- IDs are integers (auto-increment) except temporary link IDs (UUID)
- Field names use camelCase in JSON payloads
- DB column names use snake_case (mapped by backend)
- Pagination uses `page` + `limit` pattern with `total` count in response
- Auth endpoints are under `/auth/*` (no JWT required)
- Protected endpoints are under `/api/*` (JWT required)
- Internal service-to-service calls are protected by Docker network isolation (sentinel-net)
