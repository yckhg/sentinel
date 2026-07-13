# 센서 장치 생명주기(등록·삭제·재출현) 스펙

> 상태: 살아있는 계약 (living spec) · 독자: 스펙 작성자 / 오케스트레이터
> 접합부: **web-frontend ↔ web-backend**, **hw-gateway ↔ web-backend**
> 본 문서는 음성센서(장비)의 **등록·삭제·재활성 생명주기**를 계약으로 고정한다.
> Web API 접면의 전면 SSOT는 `docs/spec/interface-web-api.md`이며, 본 문서는 그 계약 중
> "센서 장치 생명주기" 단위의 의도(명시 등록·스티키 삭제·재출현 경보)를 규정한다. 충돌 시 본 단위
> 범위 안에서는 본 문서가 최신 의도를 가지며, 구현 중 `interface-web-api.md`·`web-backend.md`·`hw-gateway.md`를
> 본 계약에 맞춰 정합화한다(아래 "델타" 참조).
> 카메라 관리(및 카메라 삭제 증거 보존·녹화 reload 전파)는 별개 잎 `docs/spec/camera-change-propagation.md`가 소유한다.

## 목적 / 의도

현장에는 음성센서가 N개 존재하며 교체가 일어난다. 이 단위는 센서의 등록·삭제·재활성을 판정 가능한 계약으로 고정하여 네 가지를 보장한다.

1. **존재감과 생명주기는 직교한다** — heartbeat(존재감, `last_seen`)는 장치가 살아있음만 갱신하고, 등록/삭제(생명주기)는 절대 바꾸지 않는다. 이는 표준 디바이스 레지스트리의 원칙이며, 센서가 신호를 보낸다는 사실이 운영자의 등록 결정을 뒤집지 않게 한다.
2. **삭제는 유지된다(sticky)** — 운영자가 삭제한 센서는 삭제 상태를 유지한다. 삭제된 센서가 다시 신호를 보내면 **자동으로 되살리지 않고**, 운영자에게 **재출현 사실을 알린다**(관리자 대면 경보). 되살릴지는 운영자가 결정한다(원클릭 재활성). 교체는 이 삭제 + 명시 추가의 조합으로 표현된다(replace 전용 액션은 없다).
3. **명시 등록으로 자동발견을 보완한다** — 센서는 heartbeat 자동발견으로 등장하되, 자동발견이 놓치는 경우 운영자가 명시적으로 등록(또는 삭제된 센서를 재활성)할 수 있다.
4. **관리는 안전기능과 독립이다** — 센서 삭제는 감시 목록(등록 레지스트리)에서 제외하는 것이며, 그 센서가 발행하는 위기·후보 경보의 처리는 등록/삭제 상태와 무관하게 계속된다.

배경 의도: 센서는 자동발견만 가능해 "교체=삭제+추가" 패턴이 반쪽이었고, 삭제된 장치가 재신호로 무음 복원되어 운영자의 제거 의도가 조용히 뒤집혔다. 산업안전 도메인에서 "의도적으로 제거한 장치를 시스템이 말없이 되살리는 것"은 위험하다 — 정답은 무음 복원이 아니라 재출현 경보다. 존재감(last_seen)과 생명주기(등록/삭제)를 분리하고, 삭제를 sticky로 두되 재출현 시 운영자에게 알려 원클릭 재활성으로 마찰을 없앤다.

## 언어 · 런타임

- **web-backend**: Go (표준 `net/http`, Go 1.22+ 메서드 라우팅), 포트 `:8080`
- **web-frontend**: TypeScript + React (Vite), 브라우저 `fetch` / `WebSocket`
- **hw-gateway**: Go, MQTT 구독자 (heartbeat 자동발견 통지자)
- **직렬화**: JSON (`Content-Type: application/json`), WS는 JSON 텍스트 프레임

## 의존 도구 · 시스템

- **SQLite** — `devices` 테이블(`site_id`·`device_id`·`alias`·`first_seen`·`last_seen`·`deleted_at`, 서버 발급 surrogate PK, `UNIQUE(site_id, device_id)`).
- **JWT (HS256)** — 관리 엔드포인트 인증. role: `admin`(등록·삭제·재활성) · `user`(조회) · `temp`(read-only).
- **내부 신뢰 경계** — `POST /api/devices/seen`은 서비스 간 internal 경로다. 내부 호출자(hw-gateway)만 접근하도록 강제한다(내부 네트워크 바인딩 또는 공유 시크릿). 외부 라우팅은 차단한다.
- **WebSocket `/ws`** — 관리자 대면 통지(재출현 경보) 브로드캐스트.
- **hw-gateway** — MQTT `safety/+/heartbeat` 수신 시 web-backend `POST /api/devices/seen`으로 자동발견 통지(단방향·최선노력). 복원/삭제 판정 소유는 web-backend이며, hw-gateway는 통지자다.

## 식별자 규약

- 관리 엔드포인트의 `{id}`는 **`devices`의 서버 발급 surrogate PK**다(발행자 제공 `device_id`가 아니다). `device_id`는 site 간 유일하지 않으므로(`UNIQUE(site_id, device_id)`), 삭제·재활성은 PK로 지목한다.
- 자동발견 통지(`seen`)는 `(site_id, device_id)`로 들어오며, web-backend가 이를 PK 행으로 해석해 동일 PK의 상태를 다룬다.

---

## 계약 1 — 센서 명시 등록 / 재활성 (web-backend)

### 입력

| Method | Path | Auth | 입력 |
|--------|------|------|------|
| POST | `/api/devices` | admin | `{siteId: string, deviceId: string, alias?: string}` — `siteId`·`deviceId` 필수(비어있지 않음) |

### 출력 (계약)

- 신규 `(siteId, deviceId)`: **`201`**, 등록된 장치 표현 반환. 등록 직후는 **오프라인 대기** — `last_seen`은 미신호(예: `null`), `deleted_at`은 `null`. heartbeat가 도착하면 온라인으로 전이한다.
- 이미 존재하고 **미삭제**인 `(siteId, deviceId)`: **`409`**(중복 등록 방어).
- 이미 존재하나 **삭제(deleted)** 상태인 `(siteId, deviceId)`: **`200`**, 그 행을 **재활성**(`deleted_at` 해제)한다. `alias`가 제공되면 갱신하고, 없으면 기존 `alias`를 유지한다.

### 핵심 로직 (동작)

- `POST /api/devices`는 **생성-또는-재활성** 단일 전이다 — 신규 키는 생성하고, 삭제된 키는 되살린다. 이것이 삭제된 센서를 다시 관리 대상으로 올리는 유일한 명시 경로다(자동발견 `seen`은 삭제를 되살리지 않는다 — 계약 2).
- 명시 등록/재활성은 관리 액션이므로 admin 전용이다.

---

## 계약 2 — 자동발견 · 스티키 삭제 · 재출현 경보 (web-backend ↔ hw-gateway)

### 입력

| Method | Path | Auth | 입력 |
|--------|------|------|------|
| POST | `/api/devices/seen` | internal only | hw-gateway 자동발견 통지 — `{siteId, deviceId, ...}` (heartbeat 파생) |
| DELETE | `/api/devices/{id}` | admin | — |

### 출력 (계약)

- `POST /api/devices/seen` — **존재감만 반영, 생명주기 불변**:
  - 미지 `(siteId, deviceId)` → **자동 등록**(신규 행 생성, 온라인). 무접촉 온보딩.
  - 기지 `(siteId, deviceId)` → **`last_seen` 갱신**(온라인). `deleted_at`은 이 통지로 절대 바뀌지 않는다.
  - 기지 & **삭제 상태** → `last_seen`은 갱신하되 **삭제 상태를 유지**하고(자동 복원 없음), 삭제 후 **첫 재출현 시** 관리자 대면 재출현 경보를 1회 발생시킨다(연속 heartbeat마다 반복하지 않는다).
- `DELETE /api/devices/{id}` → 장치를 삭제(soft-delete)한다. 삭제는 **유지(sticky)** 된다 — 이후 `seen`으로 되살아나지 않는다. 되살리려면 명시적 재활성(계약 1의 `POST /api/devices`)을 쓴다.

### 출력 — 재출현 경보 (WS)

- 삭제된 장치가 다시 신호를 보내면, web-backend는 접속 중인 관리자에게 재출현을 통지한다:

```json
{ "type": "device_reappeared", "payload": { "siteId": "site-001", "deviceId": "vs-01", "lastSeen": "2026-07-13 10:20:30" } }
```

- 이 통지는 장치를 복원하지 않는다 — 운영자가 계약 1의 재활성으로 되살릴지 결정한다.

### 핵심 로직 (동작)

- **존재감·생명주기 직교** — `seen`(heartbeat 파생)은 `last_seen`만 쓰고 `deleted_at`은 결코 쓰지 않는다. 등록/삭제는 오직 관리 엔드포인트(계약 1의 POST, 계약 2의 DELETE)로만 바뀐다.
- **삭제는 sticky** — 삭제된 장치는 재신호해도 삭제를 유지한다. 원자성: `seen`은 `INSERT ... ON CONFLICT(site_id, device_id) DO UPDATE SET last_seen=…`(deleted_at 미포함)의 단일 문장으로 수행해 SELECT-후-UPDATE 경합과 UNIQUE 위반을 피한다.
- **재출현은 알린다** — 삭제 후 첫 재신호에서 관리자 재출현 경보 1회. 무음 복원을 대체한다.
- **삭제 ≠ 안전기능 정지** — 삭제는 감시 목록 제외일 뿐이다. 위기·후보 경보의 생성·전달 경로는 `devices` 등록/삭제 상태를 참조하지 않고 `site_id`·원천 키로만 동작한다(계약 3).

---

## 계약 3 — 관리와 안전기능의 독립 (web-backend)

### 출력 (계약)

- 위기·후보 경보의 처리(사고 생성·전달) 경로는 `devices` 테이블의 등록/삭제 상태를 **조회하지 않는다.** 삭제된 센서의 `site_id`로 위기 이벤트가 유입되어도 사고(incident)는 정상 생성·전달된다.

### 핵심 로직 (동작)

- 안전경보 파이프라인은 `devices.deleted_at`이나 등록 여부를 판정 입력으로 쓰지 않는다. 이 독립은 산업안전 불변식(삭제가 경보를 무음 정지시키지 않음)이다.

---

## 계약 4 — 장치 관리 UI (web-frontend)

### 출력 (계약)

- 센서/장비 관리 화면에 **"장치 추가" 액션**이 존재하여 `siteId`·`deviceId`(및 선택 별칭)를 입력해 계약 1의 `POST /api/devices`를 호출한다(카메라 관리의 "추가"와 대칭).
- 삭제 안내 문구는 sticky 삭제 의미를 정확히 반영한다 — 삭제한 장치는 다시 신호를 보내도 **자동 복원되지 않고 재출현 알림으로 안내되며**, 되살리려면 재활성한다는 취지를 알린다.
- 재출현 경보(WS `device_reappeared`)를 수신하면 관리자 화면에 그 장치의 재출현을 표시하고, **원클릭 재활성**(계약 1의 `POST /api/devices`) 경로를 제공한다.

### 핵심 로직 (동작)

- 장치 추가·삭제·재활성은 관리자 대상 액션이다(비-admin에겐 미노출 또는 비활성).

## 검증 단언 (TDD)

- **A (핵심)** — 명시 등록: 신규 `(siteId, deviceId)`에 `POST /api/devices`(admin) → `201`. 이후 `GET /api/devices`에 그 장치가 **오프라인 대기**(미신호: `last_seen`이 온라인 임계 밖 또는 미설정)로 나타나고 `deleted_at`이 없다.
  ```bash
  curl -s -X POST -H "Authorization: Bearer $ADMIN" -H 'Content-Type: application/json' \
    http://localhost:8080/api/devices -d '{"siteId":"site-001","deviceId":"vs-new-01","alias":"북문 음성센서"}' \
    -o /dev/null -w '%{http_code}\n'   # → 201
  curl -s -H "Authorization: Bearer $ADMIN" http://localhost:8080/api/devices \
    | jq -e 'any(.[]; .siteId=="site-001" and .deviceId=="vs-new-01")'
  ```
- **B** — 중복 방어: 이미 미삭제로 존재하는 `(siteId, deviceId)`에 `POST /api/devices` → `409`.
- **C (핵심)** — 재활성(단일 경로): 삭제된 `(siteId, deviceId)`에 `POST /api/devices` → `200`이고 이후 그 장치가 미삭제(`deleted_at` 없음)로 `GET /api/devices`에 나타난다. `alias`를 함께 주면 갱신되고, 주지 않으면 기존 alias가 유지된다.
- **D (핵심, mutating)** — 자동발견: 미지 `(siteId, deviceId)`로 `POST /api/devices/seen` → 그 장치가 `GET /api/devices`에 온라인으로 자동 등록된다.
- **E (핵심, mutating)** — 스티키 삭제: (1) `DELETE /api/devices/{id}`(admin) → (2) 같은 `(siteId, deviceId)`로 `POST /api/devices/seen`(재신호) 1회 이상 → (3) 그 장치는 기본 `GET /api/devices`(미삭제 목록)에 **나타나지 않는다**(삭제 유지, 자동 복원 없음).
  ```bash
  curl -s -X DELETE -H "Authorization: Bearer $ADMIN" http://localhost:8080/api/devices/$ID
  curl -s -X POST http://localhost:8080/api/devices/seen -H 'Content-Type: application/json' \
    -d '{"siteId":"'"$SITE"'","deviceId":"'"$DEV"'"}'   # 재신호
  curl -s -H "Authorization: Bearer $ADMIN" http://localhost:8080/api/devices \
    | jq -e 'all(.[]; .deviceId != "'"$DEV"'")'   # 삭제 유지(복원 안 됨)
  ```
- **F (핵심, mutating)** — 재출현 경보: 삭제된 장치가 `POST /api/devices/seen`(재신호)하면 접속 중 관리자 WS가 `type == "device_reappeared"`(payload에 그 `deviceId`) 메시지를 **1회** 수신한다. 장치는 여전히 삭제 상태이며, `last_seen`은 갱신된다(존재감 반영).
- **G (mutating)** — 존재감 갱신: 이미 미삭제로 존재하는 장치에 `POST /api/devices/seen` → `last_seen`이 갱신되어 온라인 판정되고, 새 행이 생기지 않는다(같은 PK 갱신).
- **H1 (핵심, mutating)** — 삭제≠안전정지(런타임): 삭제된 장치의 `siteId`로 위기 경보 이벤트가 유입되면 사고(incident)가 정상 생성·전달된다.
- **H2 (핵심, static — 비-mutating)** — 삭제≠안전정지(정적): 위기·후보 경보/사고 생성 코드 경로가 `devices` 테이블을 조회하지 않는다(사고 생성 경로에 `FROM devices`·`JOIN devices`·`deleted_at` 참조가 없음을 정적 스캔으로 판정). 런타임 스택 없이 회귀를 막는 이중화 게이트.
- **I (핵심)** — internal 경계: `POST /api/devices/seen`은 내부 경로다. 비내부(외부/무자격) 호출은 거부된다(내부 신뢰 수단 부재 시 `401`/`403`). 공개 라우팅으로 임의 자동등록이 되지 않는다.
- **J (핵심)** — 권한: `admin`이 아닌 토큰(`user` 및 `temp`)으로 `POST /api/devices`·`DELETE /api/devices/{id}` → `403`. 조회 `GET /api/devices`는 `user` `200`.
  ```bash
  for TOK in "$USER_TOKEN" "$TEMP_TOKEN"; do
    curl -s -o /dev/null -w '%{http_code}\n' -X POST -H "Authorization: Bearer $TOK" \
      -H 'Content-Type: application/json' http://localhost:8080/api/devices \
      -d '{"siteId":"s","deviceId":"d"}'   # → 403 (양쪽)
  done
  ```
- **K (핵심, needs-browser SKIP — 의도적)** — 장치 관리 UI: web-frontend 장비 관리 화면에 "장치 추가" 액션이 렌더되어 `POST /api/devices`를 호출하고, 삭제 안내 문구가 sticky 삭제(자동 복원되지 않고 재출현 알림으로 안내)를 정확히 반영하며, `device_reappeared` 수신 시 재출현 표시 + 원클릭 재활성 경로가 제공된다. 브라우저 관측이 필요하므로 needs-browser SKIP으로 선언한다.

## 검증 스킵 선언 (선택)

- **D · E · F · G · H1** — 사유: mutating 단언 — `/api/devices/seen` 자동발견·삭제 전이·WS 통지·실 위기 유입은 `devices` 테이블/WS에 부작용을 남긴다. **격리 스택 + `ADMIN_TOKEN`**(F/H1은 WS 관찰자/위기 주입 포함)에서 판정한다. · 중요도: **핵심(load-bearing)** · 기본 **SKIP**, 격리 스택 준비 시 판정. (A·B·C·H2·I·J는 격리 web-backend + 정적 스캔으로 상시 판정 가능하도록 설계.)
- **K** — 사유: 브라우저 렌더 관측 필요(web-frontend needs-browser). 장치추가 UI·삭제 문구·재출현 재활성은 API로 판정 불가. · 중요도: **핵심(load-bearing)** · 해소 조건: Playwright 세션 실행 시 판정(INDEX.md SKIP 해제 조건 4와 동류).

## 델타 (SSOT 정합 — 오케스트레이터 머지 반영분)

> **편집 경계 (SSOT 위임)**: 본 잎은 **코드 + 자기 `tests/` + 본 스펙 문서**를 편집한다. 아래 인터페이스/서비스 SSOT 정합은 오케스트레이터가 머지 시점에 반영한다.

- **`interface-web-api.md`**: `POST /api/devices`(admin, 생성-또는-재활성) 신설. `DELETE /api/devices/{id}`가 sticky soft-delete임을 명시. `POST /api/devices/seen` 자동발견 규칙(존재감만 갱신·생명주기 불변·삭제는 복원 안 함)과 internal 경계 명시. WS `device_reappeared` 메시지 계약 추가. `{id}`=surrogate PK 규약 명시.
- **`web-backend.md`**: devices 절에 명시 등록/재활성·sticky 삭제·재출현 경보·존재감/생명주기 직교·안전 파이프라인의 devices 비참조를 반영. 관리 엔드포인트 admin 전용(user·temp 403).
- **`hw-gateway.md`**: 자동발견 통지(`seen`)가 web-backend에서 삭제 장치를 복원시키지 않고 존재감(last_seen)만 갱신함을 상호작용 계약으로 반영(hw-gateway는 통지자, 복원/삭제 판정 소유는 web-backend).
