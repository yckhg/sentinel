# 센서 장치 생명주기(등록·삭제·재출현) 스펙

> 상태: 살아있는 계약 (living spec) · 독자: 스펙 작성자 / 오케스트레이터
> 접합부: **web-frontend ↔ web-backend**, **hw-gateway ↔ web-backend**
> 본 문서는 음성센서(장비)의 **등록·삭제·재활성 생명주기**를 계약으로 고정한다.
> Web API 접면의 전면 SSOT는 `docs/spec/interface-web-api.md`이며, 본 문서는 그 계약 중
> "센서 장치 생명주기" 단위의 의도(명시 등록·스티키 삭제·재출현 경보)를 규정한다. 충돌 시 본 단위
> 범위 안에서는 본 문서가 최신 의도를 가지며, 구현 중 `interface-web-api.md`·`web-backend.md`·`hw-gateway.md`를
> 본 계약에 맞춰 정합화한다(아래 "델타" 참조 — **정합 대상 문장·단언을 개별 열거**한다).
> 카메라 관리(및 카메라 삭제 증거 보존·녹화 reload 전파)는 별개 잎 `docs/spec/camera-change-propagation.md`가 소유한다.

## 목적 / 의도

현장에는 음성센서가 N개 존재하며 교체가 일어난다. 이 단위는 센서의 등록·삭제·재활성을 판정 가능한 계약으로 고정하여 네 가지를 보장한다.

1. **존재감과 생명주기는 직교한다** — heartbeat(존재감, `last_seen`)는 장치가 살아있음만 갱신하고, 등록/삭제(생명주기, `deleted_at`)는 절대 바꾸지 않는다. 이는 표준 디바이스 레지스트리의 원칙이며, 센서가 신호를 보낸다는 사실이 운영자의 등록 결정을 뒤집지 않게 한다.
2. **삭제는 유지된다(sticky)** — 운영자가 삭제한 센서는 삭제 상태를 유지한다. 삭제된 센서가 다시 신호를 보내면 삭제 상태를 그대로 두고, 운영자에게 **재출현 사실을 알린다**(관리자 대면 경보). 되살릴지는 운영자가 결정한다(원클릭 재활성). 교체는 `DELETE /api/devices/{id}`(삭제) 후 `POST /api/devices`(추가 또는 재활성) 두 호출로 수행한다(전용 replace 액션 없이 이 두 관리 액션의 조합으로 표현).
3. **명시 등록으로 자동발견을 보완한다** — 센서는 heartbeat 자동발견으로 등장하되, 자동발견이 놓치는 경우 운영자가 명시적으로 등록(또는 삭제된 센서를 재활성)할 수 있다.
4. **관리는 안전기능과 독립이다** — 센서 삭제는 감시 목록(등록 레지스트리)에서 제외하는 것이며, 그 센서가 발행하는 위기·후보 경보의 처리는 등록/삭제 상태와 무관하게 계속된다.

배경 의도: 현재 시스템은 삭제된 장치가 재신호(`seen`) 또는 위기 사고 생성(`incidents`) 시 `deleted_at`을 `NULL`로 초기화해 **무음 복원**한다(`devices.go` seen upsert · `incidents.go` device upsert 양쪽). 그 결과 운영자의 제거 의도가 조용히 뒤집힌다. 산업안전 도메인에서 "의도적으로 제거한 장치를 시스템이 말없이 되살리는 것"은 위험하다. 본 잎은 두 무음 복원 경로를 모두 sticky 유지로 **교체**하고, 삭제 후 첫 재출현에서 운영자에게 재출현 경보를 발행하여, 존재감(last_seen)과 생명주기(deleted_at)를 분리하고 원클릭 재활성으로 마찰을 없앤다.

## 언어 · 런타임

- **web-backend**: Go (표준 `net/http`, Go 1.22+ 메서드 라우팅), 포트 `:8080`
- **web-frontend**: TypeScript + React (Vite), 브라우저 `fetch` / `WebSocket`
- **hw-gateway**: Go, MQTT 구독자 (heartbeat 자동발견 통지자)
- **직렬화**: JSON (`Content-Type: application/json`), WS는 JSON 텍스트 프레임

## 의존 도구 · 시스템

- **SQLite** — `devices` 테이블. 기존 컬럼(서버 발급 surrogate PK `id`·`site_id`·`device_id`·`alias`·`first_seen`·`last_seen`·`deleted_at`·`alert_state`, `UNIQUE(site_id, device_id)`)에 더해 본 잎이 **두 마이그레이션을 추가**한다:
  - `last_seen`을 **nullable**로 완화한다(현재 `NOT NULL DEFAULT datetime('now')`). `NULL` = 미신호(오프라인 대기). 명시 등록 직후의 오프라인 대기를 표현하기 위함이다.
  - `reappear_alerted_at DATETIME`(nullable) 신규 컬럼 — 삭제 후 재출현 경보를 **이미 1회 발행했는지**를 영속 기록하는 dedup 상태다.
- **JWT (HS256)** — 관리 엔드포인트 인증. role: `admin`(등록·삭제·재활성·alias 수정) · `user`(조회) · `temp`(read-only). admin 부트스트랩은 기존 `ADMIN_USERNAME`/`ADMIN_PASSWORD` 수단을 따른다(별도 `ADMIN_TOKEN` 수단 없음 — 테스트는 admin 계정으로 로그인해 발급된 JWT를 쓴다).
- **내부 신뢰 경계 — 공유 시크릿 헤더** — `POST /api/devices/seen`은 서비스 간 internal 경로다. web-backend는 환경변수로 주입된 시크릿(`INTERNAL_TOKEN`)을 요청 헤더 `X-Internal-Token`으로 검증한다: 유효 시크릿 있으면 통과, 없거나 불일치면 `401`. hw-gateway는 호출 시 이 헤더를 동봉한다. (시스템의 기존 Docker 네트워크 격리에 **더하는 앱레벨 방어**이며, 이 잎이 다루는 경로는 `seen`에 한정한다.)
- **WebSocket `/ws`** — 관리자 대면 통지(재출현 경보) 브로드캐스트. 기존 `Broadcast*` 패턴(admin 필터)을 따른다.
- **hw-gateway** — MQTT `safety/+/heartbeat`(및 alert·candidate) 수신 시 web-backend `POST /api/devices/seen`으로 자동발견 통지(단방향·최선노력, `X-Internal-Token` 동봉, body `{siteId, deviceId, alertState}`). 복원/삭제 판정 소유는 web-backend이며, hw-gateway는 통지자다.

## 식별자 규약

- 관리 엔드포인트의 `{id}`는 **`devices`의 서버 발급 surrogate PK**다(발행자 제공 `device_id`가 아니다). `device_id`는 site 간 유일하지 않고 `(site_id, device_id)` 복합키만 유일하므로, URL 경로 파라미터로는 단일 정수 PK가 편하다(surrogate의 근거는 "URL 편의"이지 "유일성 확보"가 아니다 — 유일성은 복합 자연키가 이미 보장한다).
- 자동발견 통지(`seen`)와 명시 등록/재활성(`POST /api/devices`)은 `(site_id, device_id)`로 들어오며, web-backend가 이를 `UNIQUE(site_id, device_id)` 위에서 해당 행으로 해석한다. 재활성 핸들러는 `WHERE site_id=? AND device_id=?`로 정확히 지목한다.
- **경계(정체성 이월)** — sticky 삭제는 "같은 `(site_id, device_id)` = 같은 물리 장치"를 전제한다. 하드웨어 교체로 다른 물리 장치가 같은 `device_id`를 재사용하면 재활성이 옛 이력을 이월한다. 본 잎은 이 경우를 재활성 시 alias 갱신·`first_seen` 유지로만 다루며, 완전한 정체성 리셋(tombstone 영구 폐기)은 범위 밖으로 남긴다(향후 검토).

---

## 계약 1 — 센서 명시 등록 / 재활성 (web-backend)

### 입력

| Method | Path | Auth | 입력 |
|--------|------|------|------|
| POST | `/api/devices` | admin | `{siteId: string, deviceId: string, alias?: string}` — `siteId`·`deviceId` 필수(비어있지 않음). `alias`는 optional; 구현은 `*string`으로 받아 미제공(nil)과 빈 문자열을 구분한다. |

### 출력 (계약)

- 신규 `(siteId, deviceId)`: **`201`**, 등록된 장치 표현 반환. 등록 직후는 **오프라인 대기** — `last_seen`은 `NULL`(미신호), `deleted_at`은 `null`. heartbeat가 도착하면 온라인으로 전이한다.
- 이미 존재하고 **미삭제**인 `(siteId, deviceId)`: **`409`**(중복 등록 방어).
- 이미 존재하나 **삭제(deleted)** 상태인 `(siteId, deviceId)`: **`200`**, 그 행을 **재활성**한다 — `deleted_at`을 해제하고 `reappear_alerted_at`을 `NULL`로 리셋한다(다음 삭제-재출현 사이클에서 다시 1회 경보 가능). **`last_seen`은 변경하지 않는다**(기존 값 유지 — 최근 재신호가 있었으면 online, 없었으면 오프라인 대기). `alias`가 제공(non-nil)되면 갱신하고, 미제공(nil)이면 기존 `alias`를 유지한다(기존이 null이면 null 유지).

### 핵심 로직 (동작)

- `POST /api/devices`는 **생성-또는-재활성** 단일 전이다 — 신규 키는 생성하고, 삭제된 키는 되살린다. 이것이 삭제된 센서를 다시 관리 대상으로 올리는 **유일한 명시 경로**다. 기존 `POST /api/devices/{id}/restore` 엔드포인트는 **제거**하고 재활성을 이 경로로 단일화한다(델타 참조). 자동발견 `seen`·사고 생성 `incidents` 경로는 삭제를 되살리지 않는다(계약 2·3).
- 세 분기(신규 INSERT / 미삭제 409 / 삭제 재활성 200)는 `UNIQUE(site_id, device_id)` 위에서 원자적으로 판정한다.
- 명시 등록/재활성은 관리 액션이므로 admin 전용이다.

---

## 계약 2 — 자동발견 · 스티키 삭제 · 재출현 경보 (web-backend ↔ hw-gateway)

### 입력

| Method | Path | Auth | 입력 |
|--------|------|------|------|
| POST | `/api/devices/seen` | internal only (`X-Internal-Token`) | hw-gateway 자동발견 통지 — `{siteId, deviceId, alertState?}` (heartbeat 파생). `alertState` 기존 동작 보존. |
| DELETE | `/api/devices/{id}` | admin | — |

### seen 전이표 (사전상태 × 효과)

| 사전상태 | last_seen | deleted_at | 재출현 경보 |
|----------|-----------|-----------|-------------|
| 미지 `(site,device)` | 신규행 now (online) | null | — (신규 자동등록) |
| 기지 · 미삭제 | now 갱신 (online) | 불변(null) | — |
| 기지 · **삭제** | now 갱신 (존재감 반영) | **불변(non-null 유지)** | 삭제 후 **첫 재출현 시 1회** |

- `alertState`는 제공되면 기록하고(기존 `alert_state` 컬럼), 미제공이면 기존값 유지한다.

### 출력 (계약)

- `POST /api/devices/seen` — **존재감만 반영, 생명주기 불변**(위 전이표). `deleted_at`은 이 통지로 **절대** 바뀌지 않는다(현재 코드의 `deleted_at = NULL` 절을 제거한다 — 델타 참조).
- `DELETE /api/devices/{id}` → 장치를 삭제(soft-delete)한다. 삭제는 **유지(sticky)** 된다 — 이후 `seen`·`incidents`로 되살아나지 않는다. 되살리려면 계약 1의 `POST /api/devices` 재활성을 쓴다.

### 재출현 경보 (WS) — 발행 · dedup · 유실 보정

- 삭제된 장치가 다시 신호를 보내면(삭제 후 첫 `seen`), web-backend는 접속 중 관리자에게 재출현을 통지한다:

```json
{ "type": "device_reappeared", "payload": { "siteId": "site-001", "deviceId": "vs-01", "lastSeen": "2026-07-13T10:20:30Z" } }
```

- **1회 dedup** — 발행 여부는 `reappear_alerted_at`으로 영속 판정한다. 삭제 상태에서 `reappear_alerted_at IS NULL`인 seen 1건에서만 경보를 발행하고 그때 `reappear_alerted_at`을 채운다. 이후 연속 heartbeat는 재발행하지 않는다. web-backend 재시작 후에도 DB 영속이라 스팸이 없다. 재활성 시 `reappear_alerted_at`이 리셋되므로(계약 1), 재활성→재삭제→재출현 사이클에서는 다시 1회 발행된다.
- **유실 보정(backfill)** — 재출현 순간 접속 관리자가 없을 수 있다. 관리자 WS가 (재)접속하면, web-backend는 현재 `deleted_at IS NOT NULL AND reappear_alerted_at IS NOT NULL`인 장치들의 재출현을 스냅샷으로 재전달한다(health 통지의 재접속 스냅샷 재전달과 동형). 이로써 무접속 중 발생한 재출현이 영구 유실되지 않는다.
- 이 통지는 장치를 복원하지 않는다 — 운영자가 계약 1의 재활성으로 되살릴지 결정한다.

### 핵심 로직 (동작)

- **존재감·생명주기 직교** — `seen`(heartbeat 파생)은 `last_seen`(및 `reappear_alerted_at` dedup 전이)만 쓰고 `deleted_at`은 결코 쓰지 않는다. 등록/삭제는 오직 관리 엔드포인트(계약 1의 POST, 계약 2의 DELETE)로만 바뀐다.
- **원자성 · 재출현 판정** — `seen`은 단일 UPSERT 문장으로 수행해 SELECT-후-UPDATE 경합과 UNIQUE 위반을 피한다. 재출현 dedup은 같은 문장 안에서 조건부로 처리한다 — 예:
  ```sql
  INSERT INTO devices (site_id, device_id, last_seen, alert_state)
  VALUES (?, ?, datetime('now'), ?)
  ON CONFLICT(site_id, device_id) DO UPDATE SET
    last_seen = datetime('now'),
    alert_state = excluded.alert_state,
    reappear_alerted_at = CASE
      WHEN deleted_at IS NOT NULL AND reappear_alerted_at IS NULL THEN datetime('now')
      ELSE reappear_alerted_at END
  RETURNING deleted_at, (deleted_at IS NOT NULL AND reappear_alerted_at = last_seen) AS just_alerted;
  ```
  `just_alerted`가 참인 응답에서만 `device_reappeared`를 브로드캐스트한다(원자적 first-detection). 구현은 이 의미(삭제 상태 첫 재출현 1회)를 지키는 한 동등한 문장으로 대체 가능하다.
- **삭제는 sticky** — 삭제된 장치는 재신호해도 삭제를 유지한다.
- **삭제 ≠ 안전기능 정지** — 계약 3.

---

## 계약 3 — 관리와 안전기능의 독립 (web-backend)

### 출력 (계약)

- 위기·후보 경보의 **처리(사고 생성·전달)** 경로는 `devices` 테이블의 등록/삭제 상태를 **판정 입력으로 조회하지 않는다.** 삭제된 센서의 `site_id`·`deviceId`로 위기 이벤트가 유입되어도 사고(incident)는 정상 생성·전달된다.
- 사고 생성 경로가 존재감 기록을 위해 `devices`에 **write(presence upsert)** 하더라도, 그 write는 **sticky를 준수한다** — `deleted_at`을 해제하지 않는다(현재 `incidents.go`의 `deleted_at = NULL` 절을 제거한다 — 델타 참조). 삭제된 센서가 위기를 발행하면 사고는 생성되되 장치는 삭제 유지이며, 삭제 후 첫 유입이면 계약 2의 재출현 경보 dedup을 동일하게 적용한다.

### 핵심 로직 (동작)

- 안전경보 파이프라인은 `devices.deleted_at`이나 등록 여부를 **사고 생성·전달의 게이팅 조건**으로 쓰지 않는다(read 게이트 부재). 동시에 사고 생성의 부수적 device presence upsert는 **삭제를 되살리지 않는다**(write sticky). 이 두 불변식(read 비게이팅 + write sticky)이 "삭제가 경보를 무음 정지시키지 않으면서도, 경보가 삭제를 무음 복원시키지도 않는다"를 함께 보장한다.

---

## 계약 4 — 장치 관리 UI (web-frontend)

### 출력 (계약)

- 센서/장비 관리 화면에 **"장치 추가" 액션**이 존재하여 `siteId`·`deviceId`(및 선택 별칭)를 입력해 계약 1의 `POST /api/devices`를 호출한다(카메라 관리의 "추가"와 대칭).
- 삭제 안내 문구는 sticky 삭제 의미를 정확히 반영한다 — 삭제한 장치는 다시 신호를 보내도 자동 복원되지 않고 재출현 알림으로 안내되며, 되살리려면 재활성한다는 취지를 알린다.
- 재출현 경보(WS `device_reappeared`)를 수신하면 관리자 화면에 그 장치의 재출현을 표시하고, **원클릭 재활성**(계약 1의 `POST /api/devices`) 경로를 제공한다. (기존 `/restore` 호출은 `POST /api/devices`로 대체한다.)

### 핵심 로직 (동작)

- 장치 추가·삭제·재활성은 관리자 대상 액션이다(비-admin에겐 미노출 또는 비활성).
- `GET /api/devices` 응답의 `lastSeen`은 `string | null`이며(오프라인 대기 = null), 프론트는 null을 오프라인으로 렌더한다.

## 검증 단언 (TDD)

- **A (핵심)** — 명시 등록: 신규 `(siteId, deviceId)`에 `POST /api/devices`(admin) → `201`. 이후 `GET /api/devices`에 그 장치가 **오프라인 대기**(`lastSeen == null`)로 나타나고 `deletedAt`이 없다. (온라인 판정 기준은 `interface-web-api.md` health 계약의 `sensor_alive_threshold_sec`이며, `lastSeen == null`은 이 판정에서 항상 오프라인으로 취급되고 health를 unhealthy로 오판정시키지 않는다.)
  ```bash
  curl -s -X POST -H "Authorization: Bearer $ADMIN" -H 'Content-Type: application/json' \
    http://localhost:8080/api/devices -d '{"siteId":"site-001","deviceId":"vs-new-01","alias":"북문 음성센서"}' \
    -o /dev/null -w '%{http_code}\n'   # → 201
  curl -s -H "Authorization: Bearer $ADMIN" http://localhost:8080/api/devices \
    | jq -e 'any(.[]; .siteId=="site-001" and .deviceId=="vs-new-01" and .lastSeen==null and (.deletedAt|not))'
  ```
- **B** — 중복 방어: 이미 미삭제로 존재하는 `(siteId, deviceId)`에 `POST /api/devices` → `409`.
- **C (핵심)** — 재활성(단일 경로): 삭제된 `(siteId, deviceId)`에 `POST /api/devices` → `200`이고 이후 그 장치가 미삭제(`deletedAt` 없음)로 `GET /api/devices`에 나타난다. `last_seen`은 변경되지 않는다(기존 값 유지). `alias`를 함께 주면 갱신되고(빈 문자열 포함), 주지 않으면 기존 alias가 유지된다. 재활성으로 `reappear_alerted_at`이 리셋된다.
- **C2 (핵심, static — 비-mutating)** — 재활성 단일 경로: 코드/라우팅에 `POST /api/devices/{id}/restore` 라우트·핸들러가 **존재하지 않음**을 정적 확인(재활성은 `POST /api/devices` 단일 경로).
- **D (핵심, mutating)** — 자동발견: 미지 `(siteId, deviceId)`로 `POST /api/devices/seen`(`X-Internal-Token` 포함) → 그 장치가 `GET /api/devices`에 온라인으로 자동 등록된다.
- **E (핵심, mutating)** — 스티키 삭제: (1) `DELETE /api/devices/{id}`(admin) → (2) 같은 `(siteId, deviceId)`로 `POST /api/devices/seen`(재신호) 1회 이상 → (3) 그 장치는 기본 `GET /api/devices`(미삭제 목록)에 **나타나지 않는다**(삭제 유지, 자동 복원 없음).
  ```bash
  curl -s -X DELETE -H "Authorization: Bearer $ADMIN" http://localhost:8080/api/devices/$ID
  curl -s -X POST http://localhost:8080/api/devices/seen -H "X-Internal-Token: $INTERNAL_TOKEN" \
    -H 'Content-Type: application/json' -d '{"siteId":"'"$SITE"'","deviceId":"'"$DEV"'"}'   # 재신호
  curl -s -H "Authorization: Bearer $ADMIN" http://localhost:8080/api/devices \
    | jq -e 'all(.[]; .deviceId != "'"$DEV"'")'   # 삭제 유지(복원 안 됨)
  ```
- **E2 (핵심, mutating)** — 사고 경로 sticky: (1) 장치 삭제 → (2) 같은 `(siteId, deviceId)`로 `POST /api/incidents` 위기 사고 생성 → (3) 사고는 정상 생성되되(계약 3) 그 장치는 여전히 삭제 상태(`GET /api/devices` 미출현). 사고 생성 경로의 device presence upsert가 `deleted_at`을 되살리지 않음을 검증(현재 `incidents.go`의 무음 복원 회귀 방지).
- **F (핵심, mutating)** — 재출현 경보 1회: 삭제된 장치가 `POST /api/devices/seen`(재신호)하면 접속 중 관리자 WS가 `type == "device_reappeared"`(payload에 그 `deviceId`) 메시지를 **1회** 수신한다. 연속 재신호 2회 이상에도 추가 발행 없음(1회 dedup). 장치는 여전히 삭제 상태이며 `last_seen`은 갱신된다. 재활성 후 재삭제-재신호 시 다시 1회 발행됨(dedup 리셋).
- **F2 (mutating)** — 재출현 backfill: 관리자 미접속 중 삭제 장치가 재신호 → 이후 관리자 WS 신규 접속 시 그 장치의 재출현이 스냅샷으로 재전달된다.
- **G (mutating)** — 존재감 갱신: 이미 미삭제로 존재하는 장치에 `POST /api/devices/seen` → `last_seen`이 갱신되어 온라인 판정되고, 새 행이 생기지 않는다(같은 PK 갱신). `deleted_at` 불변(null 유지).
- **H1 (핵심, mutating)** — 삭제≠안전정지(런타임): 삭제된 장치의 `siteId`·`deviceId`로 위기 경보 이벤트가 유입되면 사고(incident)가 정상 생성·전달된다.
- **H2 (핵심, static — 비-mutating)** — 삭제≠안전정지(정적, 이중 판정):
  - (read) 사고 생성·전달 코드 경로가 `devices.deleted_at`을 **게이팅 조건으로 조회하지 않음**(사고 생성 경로에 `deleted_at IS NULL` 류 필터가 사고 발행을 막지 않음).
  - (write) 사고 생성 경로의 device presence upsert가 `deleted_at`을 **해제하지 않음**(`incidents.go` 사고 생성 경로에 `deleted_at = NULL` write가 없음). 런타임 스택 없이 무음 복원 회귀를 막는 정적 게이트. (import/의존 그래프 또는 사고 생성 함수 본문 스캔으로 판정; 텍스트 grep의 위양성/위음성을 줄이기 위해 사고 생성 핸들러 범위로 스코프를 좁힌다.)
- **I (핵심)** — internal 경계: `POST /api/devices/seen`은 `X-Internal-Token` 검증을 거친다. 헤더 부재·불일치 호출은 `401`. 유효 시크릿 호출은 통과. 공개 라우팅으로 임의 자동등록이 되지 않는다.
  ```bash
  curl -s -o /dev/null -w '%{http_code}\n' -X POST http://localhost:8080/api/devices/seen \
    -H 'Content-Type: application/json' -d '{"siteId":"s","deviceId":"d"}'   # → 401 (시크릿 없음)
  curl -s -o /dev/null -w '%{http_code}\n' -X POST http://localhost:8080/api/devices/seen \
    -H "X-Internal-Token: $INTERNAL_TOKEN" -H 'Content-Type: application/json' \
    -d '{"siteId":"s","deviceId":"d"}'   # → 2xx (유효 시크릿)
  ```
- **J (핵심)** — 권한: 모든 생명주기 변이는 admin 전용이다. `admin`이 아닌 토큰(`user`·`temp`)으로 `POST /api/devices`·`DELETE /api/devices/{id}`·`PATCH /api/devices/{id}`(alias) → `403`. 조회 `GET /api/devices`는 `user` `200`.
  ```bash
  for TOK in "$USER_TOKEN" "$TEMP_TOKEN"; do
    curl -s -o /dev/null -w '%{http_code}\n' -X POST -H "Authorization: Bearer $TOK" \
      -H 'Content-Type: application/json' http://localhost:8080/api/devices \
      -d '{"siteId":"s","deviceId":"d"}'   # → 403 (양쪽)
  done
  ```
- **K (핵심, needs-browser SKIP — 의도적)** — 장치 관리 UI: web-frontend 장비 관리 화면에 "장치 추가" 액션이 렌더되어 `POST /api/devices`를 호출하고, 삭제 안내 문구가 sticky 삭제를 정확히 반영하며, `device_reappeared` 수신 시 재출현 표시 + 원클릭 재활성 경로(`POST /api/devices`)가 제공되고, `lastSeen == null` 장치가 오프라인으로 렌더된다. 브라우저 관측이 필요하므로 needs-browser SKIP으로 선언한다.

## 검증 스킵 선언 (선택)

- **D · E · E2 · F · F2 · G · H1** — 사유: mutating 단언 — `/api/devices/seen` 자동발견·삭제 전이·WS 통지·실 위기 유입은 `devices` 테이블/WS에 부작용을 남긴다. **격리 스택 + admin JWT + `INTERNAL_TOKEN`**(F/F2는 WS 관찰자, H1/E2는 위기 주입 포함)에서 판정한다. · 중요도: **핵심(load-bearing)** · 기본 **SKIP**, 격리 스택 준비 시 판정. (A·B·C·C2·H2·I·J는 격리 web-backend + 정적 스캔으로 상시 판정 가능하도록 설계. I는 `X-Internal-Token` 도입으로 상시 판정 가능해졌다.)
- **K** — 사유: 브라우저 렌더 관측 필요(web-frontend needs-browser). 장치추가 UI·삭제 문구·재출현 재활성·null 렌더는 API로 판정 불가. · 중요도: **핵심(load-bearing)** · 해소 조건: Playwright 세션 실행 시 판정(INDEX.md SKIP 해제 조건 4와 동류).

## 델타 (SSOT 정합 — 오케스트레이터 머지 반영분)

> **편집 경계 (SSOT 위임)**: 본 잎은 **코드 + 자기 `tests/` + 본 스펙 문서**를 편집한다. 아래 인터페이스/서비스 SSOT 정합은 오케스트레이터가 머지 시점에 반영한다. **기존 SSOT가 정반대(자동복원)를 계약·단언으로 못박고 있으므로, 뒤집히는 개별 문장·단언을 아래에 열거**한다(미열거 시 신·구 단언이 상호배타로 게이트가 영구 적색이 된다).

- **`interface-web-api.md`** (계약 6 Devices, 및 계약 13):
  - `POST /api/devices`(admin, 생성-또는-재활성 201/409/200) **신설**.
  - `DELETE /api/devices/{id}` 권한을 `user` → **`admin`**으로 강화. `PATCH`(alias)도 admin.
  - **자동복원 불변식 제거**: "삭제는 soft delete — heartbeat/alert가 다시 오면 자동 복원"(현 L340 취지)을 **sticky 삭제 + 재출현 경보**로 교체.
  - **자동복원 단언 제거·플립**: "soft-deleted device에 `POST /api/devices/seen` → `GET /api/devices` 재등장(`deletedAt == null`)"(현 L347) 단언을 **"재등장하지 않음(삭제 유지)"**로 플립.
  - `POST /api/devices/seen` 계약(현 L661 UPSERT `deleted_at = NULL` 멱등)에서 **`deleted_at = NULL`을 제거**하고 존재감만 갱신·재출현 dedup을 기술. internal 경계를 **`X-Internal-Token` 검증(부재 시 401)**으로 명시(현 L25 "네트워크 격리 의존, 프록시 미구성" 서술에 앱레벨 방어 추가).
  - `POST /api/incidents`의 deviceId best-effort device UPSERT(현 L658 "seen과 동일 의미론, 자동복원")를 **sticky 준수(자동복원 안 함)**로 정정.
  - WS `device_reappeared` 메시지 계약 + backfill 추가. `{id}`=surrogate PK 규약, `lastSeen: string|null` 명시.
- **`web-backend.md`**: devices 절에 명시 등록/재활성·sticky 삭제·재출현 경보(dedup·backfill)·존재감/생명주기 직교·안전 파이프라인의 read 비게이팅 + write sticky를 반영. **단언 K(현 L107 "soft-delete된 장비 seen → `deleted_at=NULL`")를 sticky(seen 후 `deleted_at` non-NULL 유지)로 재작성.** 관리 엔드포인트 admin 전용(user·temp 403). `last_seen` nullable·`reappear_alerted_at` 스키마 반영.
- **`hw-gateway.md`**: 자동발견 통지(`seen`)가 web-backend에서 삭제 장치를 복원시키지 않고 존재감(last_seen)만 갱신함을 상호작용 계약으로 반영(hw-gateway는 통지자). hw-gateway가 `seen` 호출 시 **`X-Internal-Token`을 동봉**함을 명시.
- **마이그레이션(코드, 본 잎 소유)**: `devices.last_seen`을 nullable로 완화 + `reappear_alerted_at DATETIME` 추가. health 집계/카테고리 함수(`health.go`·`health_summary.go`·`equipment.go`)의 `last_seen` null 처리를 "null = 오프라인(unhealthy 오판 아님)"으로 정합.
