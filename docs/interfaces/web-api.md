# Web API — Sentinel

> **이 문서는 Sentinel web-backend HTTP/WebSocket 인터페이스의 SSOT입니다.**
> web-frontend / 모바일 클라이언트 / 외부 통합 세션이 이 문서 하나로 backend와 통신할 수 있습니다.
> 코드와 동기화 의무 — `services/web-backend/` 라우트/응답 변경 시 본 문서도 같은 커밋에 수정.

---

## 1. 베이스 URL & 인증

- **베이스**: web-backend는 `:8080`에서 리스닝. 프록시 경유 시 `/api/...`, `/ws`, `/healthz`, `/view/...`, `/internal/...` 경로 사용.
- **인증 스킴**: JWT Bearer — `Authorization: Bearer <token>`
- **토큰 발급**: `POST /auth/login` (24시간 유효, HS256)
- **임시 링크 토큰**: `POST /api/links/temp`로 발급 (24시간 유효, read-only viewer 권한)
- **role 구분**: `admin`, `user`, `viewer`(=temp link). 일부 엔드포인트는 admin 전용
- **WebSocket 인증**: `?token=<jwt>` 쿼리 파라미터 (일반 JWT 또는 temp link JWT 모두 수용)

### 응답 공통 규칙

- 성공: `200 OK` 또는 `201 Created` / `204 No Content`, JSON body
- 에러: `{"error": "<message>"}` + 적절한 4xx/5xx 상태코드
- Content-Type: `application/json`

### 에러 상태코드 매핑

| 상태 | 의미 |
|------|------|
| 400 | 잘못된 요청 본문/파라미터 |
| 401 | 인증 헤더 누락/무효/만료 |
| 403 | 권한 부족 (admin 필요 등) |
| 404 | 리소스 없음 |
| 409 | 상태 충돌 (예: 이미 resolved된 incident) |
| 429 | 레이트 리밋 초과 (login/register 한정) |
| 502 | 하위 서비스(recording, hw-gateway) 통신 실패 |

---

## 2. 인증 / 사용자

| Method | Path | Auth | 설명 |
|--------|------|------|------|
| POST | `/auth/register` | public (5 req/min/IP) | 회원가입. `inviteToken`이 유효하면 자동 승인(active), 아니면 `pending` |
| POST | `/auth/login` | public (10 req/min/IP) | 로그인 → JWT 발급 |
| POST | `/api/auth/change-password` | user | 본인 비밀번호 변경 |
| GET | `/auth/pending` | admin | 승인 대기 사용자 목록 |
| POST | `/auth/approve/{userId}` | admin | 사용자 승인 → status=active |
| POST | `/auth/reject/{userId}` | admin | 사용자 거절 → status=rejected |
| GET | `/auth/users` | admin | 활성 사용자 목록 |

**주의**: `/auth/pending`, `/auth/approve/*`, `/auth/reject/*`, `/auth/users`는 `/api/` 프리픽스가 **아님**. `authMiddleware` 밖에서 각 핸들러가 직접 admin JWT를 검증한다.

### POST /auth/register

Request:
```json
{
  "username": "string (required)",
  "password": "string (min 8 chars, required)",
  "confirmPassword": "string (must match password)",
  "name": "string (required)",
  "inviteToken": "string (optional — valid 시 auto-approve + email 주입)"
}
```

Response `201`:
```json
{
  "id": 42,
  "username": "alice",
  "name": "Alice",
  "email": "alice@example.com",
  "status": "pending | active"
}
```

에러: `409` username/email 중복, `400` 검증 실패.

### POST /auth/login

Request:
```json
{ "username": "alice", "password": "secret123" }
```

Response `200`:
```json
{
  "token": "<JWT>",
  "user": { "id": 42, "username": "alice", "role": "user | admin" }
}
```

에러: `401` 자격 증명 불일치, `403` `account pending approval`.

### POST /api/auth/change-password

Request:
```json
{ "currentPassword": "...", "newPassword": "min 8 chars" }
```

Response `200`: `{"message": "password changed successfully"}`

### GET /auth/pending (admin)

Response `200`: 배열
```json
[{ "id": 1, "username": "bob", "name": "Bob", "email": "", "status": "pending", "createdAt": "2026-01-01 00:00:00" }]
```

### POST /auth/approve/{userId}, /auth/reject/{userId} (admin)

Response `200`: `{"id": 1, "status": "active" | "rejected"}`

### GET /auth/users (admin)

Response `200`: 배열 of `{id, username, name, email, role, createdAt}`

---

## 3. Incidents (위급 이벤트)

| Method | Path | Auth | 설명 |
|--------|------|------|------|
| POST | `/api/incidents` | **none (internal)** | hw-gateway가 호출. 생성 후 `crisis_alert` WS 브로드캐스트 |
| GET | `/api/incidents` | user | 페이지네이션된 이력 |
| PATCH | `/api/incidents/{id}/acknowledge` | admin | 확인 처리 |
| PATCH | `/api/incidents/{id}/resolve` | admin | 해결 처리 (resolution_notes 필수) |

### GET /api/incidents

Query: `page` (default 1), `limit` (default 20, max 100), `from`, `to` (occurred_at 필터, SQLite datetime 포맷), `status` (`open|acknowledged|resolved`).

Response `200`:
```json
{
  "data": [
    {
      "id": 12,
      "siteId": "site-001",
      "description": "gas leak",
      "occurredAt": "2026-04-13 10:20:30",
      "confirmedAt": "2026-04-13 10:21:00",
      "confirmedBy": "alice",
      "isTest": false,
      "status": "open | acknowledged | resolved",
      "resolvedAt": null,
      "resolvedBy": null,
      "resolutionNotes": null
    }
  ],
  "pagination": { "page": 1, "limit": 20, "total": 57 }
}
```

### POST /api/incidents (internal)

Request:
```json
{ "siteId": "site-001", "description": "...", "occurredAt": "2026-04-13 10:20:30", "isTest": false }
```
Response `201`: `{id, siteId, description, occurredAt}`. 부작용: 모든 WS 클라이언트에 `crisis_alert` 브로드캐스트.

### PATCH /api/incidents/{id}/acknowledge (admin)

Body: 없음. Response `200`: `{"status": "acknowledged"}`. `resolved`면 `409`.

### PATCH /api/incidents/{id}/resolve (admin)

Request: `{"resolutionNotes": "string (required, non-empty)"}`
Response `200`: `{"status": "resolved"}`. 이미 해결되었으면 `409`. 성공 시 recording 서비스의 아카이브 finalize를 비동기 트리거.

---

## 4. Cameras

| Method | Path | Auth | 설명 |
|--------|------|------|------|
| GET | `/api/cameras` | user | 카메라 목록 + 스트리밍 상태 |
| POST | `/api/cameras` | admin | 생성 (streamKey 자동 발급) |
| PUT | `/api/cameras/{id}` | admin | 부분 업데이트 (streamKey 불변) |
| DELETE | `/api/cameras/{id}` | admin | 삭제 |

### Camera object

```json
{
  "id": 1,
  "name": "Entry Cam",
  "location": "Main gate",
  "zone": "Zone A",
  "streamKey": "cam-a1b2c3d4",
  "sourceType": "rtsp | youtube",
  "sourceUrl": "rtsp://... or https://youtube.com/...",
  "enabled": true,
  "hlsUrl": "http://streaming/.../index.m3u8",
  "status": "connected | disconnected"
}
```

`hlsUrl`과 `status`는 streaming 서비스에서 10초 캐시로 조회해 병합.

### POST/PUT 요청 바디

```json
{
  "name": "string",
  "location": "string",
  "zone": "string",
  "sourceType": "rtsp | youtube",
  "sourceUrl": "string",
  "enabled": true
}
```

**검증**:
- `sourceType`: `rtsp` 또는 `youtube`만 허용
- RTSP: `rtsp://` 또는 `rtsps://` 스킴 필수
- YouTube: `https://(www.)youtube.com/watch?v=...` 또는 `https://youtu.be/...`
- SSRF 차단: hostname이 loopback/private/link-local IP면 거절
- PUT은 비어있지 않은 필드만 적용 (partial update)

DELETE 성공 시 `204`. 변경 시 cctv-adapter, youtube-adapter에 비동기 reload 트리거.

---

## 5. Sites

| Method | Path | Auth | 설명 |
|--------|------|------|------|
| GET | `/api/sites` | admin | 현장 목록 |
| PUT | `/api/sites/{id}` | admin | 부분 업데이트 |

Site object:
```json
{ "id": 1, "address": "...", "managerName": "...", "managerPhone": "010-1234-5678" }
```

`managerPhone`은 `01[016789]-\d{3,4}-\d{4}` 포맷 검증. 빈 문자열로 보낸 필드는 업데이트 안 됨 (partial).

---

## 6. Contacts

| Method | Path | Auth | 설명 |
|--------|------|------|------|
| GET | `/api/contacts` | user (authenticated) + internal | 비상연락망 목록 |
| POST | `/api/contacts` | admin | 생성 |
| PUT | `/api/contacts/{id}` | admin | 부분 업데이트 |
| DELETE | `/api/contacts/{id}` | admin | 삭제 |

Contact object:
```json
{
  "id": 1,
  "name": "김관리",
  "phone": "010-1234-5678",
  "email": "manager@example.com",
  "notifyEmail": true
}
```

phone 포맷: `01[016789]-\d{3,4}-\d{4}`. DELETE 성공 `204`.

**주의**: `/api/contacts` GET은 `mux`와 `apiMux` 양쪽에 등록되어 있다. Go 1.22 ServeMux는 후자(more specific 미들웨어 래핑된 쪽)가 `/api/` prefix 매칭으로 우선. 실질적으로 JWT 필요.

---

## 7. Equipment / Restart

| Method | Path | Auth | 설명 |
|--------|------|------|------|
| POST | `/api/equipment/restart` | user | hw-gateway로 장비 재부팅 명령 프록시 |
| POST | `/api/test-alert` | admin | hw-gateway 테스트 알람 트리거 |

### POST /api/equipment/restart

Request:
```json
{ "siteId": "site-001", "deviceId": "DEV-001", "reason": "optional" }
```
Response: hw-gateway 응답을 그대로 중계 (상태코드 포함). 통신 실패 시 `502`.

### POST /api/test-alert (admin)

Body 불필요. hw-gateway `/api/test-alert`에 `{siteId:"test", deviceId:"TEST-DEVICE"}` 전달.

---

## 8. Recordings / Archives / Storage

전부 recording 서비스로 프록시되는 엔드포인트 (JWT 필요). 구체 응답 스키마는 recording 서비스의 API 문서 참조(TBD — 별도 SSOT 필요).

| Method | Path | 설명 |
|--------|------|------|
| GET | `/api/recordings/{stream_key}` | 녹화 메타/목록 |
| GET | `/api/recordings/{stream_key}/play` | HLS 재생 플레이리스트 |
| GET | `/api/recordings/{stream_key}/segments/{filename}` | HLS 세그먼트 |
| GET | `/api/archives` | 아카이브 목록 |
| POST | `/api/archives` | 아카이브 생성 요청 |
| DELETE | `/api/archives/{id}` | 아카이브 삭제 |
| DELETE | `/api/archives/incident/{incidentId}` | incident별 아카이브 일괄 삭제 |
| GET | `/api/archives/{id}/download` | 아카이브 다운로드 (바이너리 스트림) |
| GET | `/api/storage` | 스토리지 사용량 통계 |

요청/응답 body는 recording 서비스 통과 원본. HLS(`application/vnd.apple.mpegurl`) 컨텐츠는 그대로 forward.

---

## 9. Links (임시 공유 링크)

admin이 뷰어 전용 read-only 링크를 발급/회수.

| Method | Path | Auth | 설명 |
|--------|------|------|------|
| POST | `/api/links/temp` | admin (또는 internal, 헤더 없으면) | 임시 링크 생성 |
| GET | `/api/links/verify/{token}` | public | 토큰 유효성 검사 |
| GET | `/api/links` | admin | 활성 링크 목록 |
| DELETE | `/api/links/{id}` | admin | 링크 회수 (blacklist) |

### POST /api/links/temp

Request: `{"label": "optional string"}`
Response `201`:
```json
{
  "id": "uuid",
  "token": "<JWT>",
  "url": "http://<site_url>/view/<token>",
  "expiresAt": "2026-04-14T10:20:30Z"
}
```

만료: 24시간 고정. `site_url`은 system_settings에서 조회, 없으면 `FRONTEND_URL` env 사용.

### GET /api/links/verify/{token}

Response `200`: `{"valid": true, "expiresAt": "..."}`. 만료/회수 시 `401`.

### GET /api/links

Response `200`: 활성(만료·회수 제외) 링크 배열
```json
[{ "id": "uuid", "label": "...", "createdAt": "...", "expiresAt": "..." }]
```

### DELETE /api/links/{id}

Response `204`. 블랙리스트에 추가 (JWT 자체는 여전히 유효하나 서버가 거부).

---

## 10. Invitations

| Method | Path | Auth | 설명 |
|--------|------|------|------|
| POST | `/api/invitations` | admin | 초대 생성 + 이메일 발송 |
| GET | `/api/invitations` | admin | 초대 목록 |
| DELETE | `/api/invitations/{id}` | admin | 초대 취소 (pending만) |
| GET | `/api/invitations/verify/{token}` | public | 초대 토큰 검증 (등록 페이지용) |

### POST /api/invitations

Request: `{"email": "user@example.com"}`
Response `201`:
```json
{
  "id": 1, "email": "user@example.com", "token": "<64 hex>",
  "status": "pending",
  "createdAt": "2026-04-13 10:20:30",
  "expiresAt": "2026-04-20 10:20:30"
}
```
유효기간 7일. notifier로 `<site_url>/register?invite=<token>` 링크 이메일 비동기 발송.

### GET /api/invitations

Response `200`: 배열. `pending`이지만 만료된 것은 응답에서 `status: "expired"`로 변환.

### DELETE /api/invitations/{id}

Response `204`. `pending`이 아니면 `404`.

### GET /api/invitations/verify/{token}

Response `200`: `{"email": "...", "status": "valid"}`. 만료/상태 이상 시 `400`, 없으면 `404`.

---

## 11. Settings

| Method | Path | Auth | 설명 |
|--------|------|------|------|
| GET | `/api/settings` | admin | 시스템 설정 목록 |
| PUT | `/api/settings/{key}` | admin | 설정 업데이트 (기존 키만) |

현재 알려진 key: `site_url` (외부에서 보이는 사이트 URL. 임시 링크/초대 이메일에 사용).

### GET /api/settings

Response `200`: `[{"key": "site_url", "value": "https://...", "updatedAt": "..."}]`

### PUT /api/settings/{key}

Request: `{"value": "string"}`
Response `200`: `{"key", "value", "updatedAt"}`. 없는 key면 `404`.

---

## 12. Internal (비인증, Docker 네트워크 한정)

외부 클라이언트는 호출 금지. nginx에서 차단되어야 함.

| Method | Path | 설명 |
|--------|------|------|
| GET | `/internal/cameras` | cctv-adapter reload용 카메라 목록 |
| GET | `/internal/settings/{key}` | 다른 서비스용 settings 조회 |
| GET | `/healthz` | `{"status":"ok","service":"web-backend"}` |
| GET | `/api/healthz` | 인증된 헬스체크 (user 정보 에코) |

---

## 13. WebSocket

- **URL**: `/ws?token=<jwt>` (ws:// 또는 wss://)
- **인증**: query param `token` — 일반 JWT 또는 temp link JWT 수용. 없거나 만료/회수 시 `401`로 업그레이드 거절
- **프로토콜**: JSON 텍스트 프레임. 서버→클라 단방향 (클라 메시지는 읽히지만 무시됨 — keep-alive 용)
- **핑/퐁**: 서버가 30초마다 ping 송신. 클라이언트는 40초 내 pong 응답해야 연결 유지
- **역할**: 접속 시 JWT에서 추출: `admin`, `user`, 또는 `temp` (temp link). 일부 메시지는 admin 전용

### 메시지 envelope

```json
{
  "type": "string",
  "payload": { ... },
  "timestamp": "2026-04-13T10:20:30Z"
}
```

### 메시지 타입

| type | 수신 대상 | payload |
|------|----------|---------|
| `connected` | 본인 | `{userId, role, connectedAt}` |
| `crisis_alert` | 전체 (admin/user/temp) | `{incidentId, siteId, description, occurredAt, isTest, site:{address,managerName,managerPhone}}` |
| `system_alarm` | admin 전용 | 임의 payload (코드에 실제 송신 지점 없음 — TBD) |

`crisis_alert`는 `POST /api/incidents` 생성 시 자동 발생.

---

## 14. TBD / 불확실 항목

- **`system_alarm` WS 메시지**: `BroadcastSystemAlarm` 함수는 정의되어 있으나 web-backend 내부에서 호출 지점 없음. 다른 서비스가 트리거할 가능성 있으나 현재 계약 확정 불가.
- **Recording/Archives/Storage 응답 스키마**: web-backend는 순수 프록시이므로 본 문서 범위 밖. recording 서비스 문서 필요.
- **`/api/contacts` GET 중복 등록**: `mux`와 `apiMux` 양쪽 등록. 동작은 `apiMux` (인증 필요) 우선이지만 의도된 설계인지 확인 필요.
- **`POST /api/incidents` 인증**: 코드상 인증 검사가 없음 (internal 가정). nginx 레벨에서 외부 차단해야 함 — 배포 설정 확인 필요.
- **rate limit 대상 범위**: 현재 `/auth/login`, `/auth/register`만 적용. 기타 엔드포인트는 레이트 리밋 없음.
