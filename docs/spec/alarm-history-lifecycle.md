# 경보 이력 + 처리 상태머신 스펙

> 상태: 살아있는 계약 (living spec) · 독자: 스펙 작성자 / 오케스트레이터
> 접합부: **web-backend ↔ web-frontend** (+ web-backend ↔ hw-gateway 센서 해소 경로)
> 본 문서는 위급 이벤트(내부 엔티티명 `incident`)의 **처리 수명주기**와 **사용자 대면 표기**를 계약으로 고정한다.
> Web API 접면의 전면 SSOT는 `docs/spec/interface-web-api.md`(계약 2·13·14)이며, 본 문서는 그 계약 중
> "경보 이력 + 처리 상태머신" 단위의 **의도 변경분(상태집합 축소·해제노트 선택화·표기 리네임)**을 규정한다.
> 두 문서가 충돌하면, 본 단위 범위(상태집합/해제/표기) 안에서는 본 문서가 최신 의도를 갖는다 —
> 구현 중 `interface-web-api.md`를 본 계약에 맞춰 정합화한다(아래 "API 계약 델타" 참조).

## 목적 / 의도

위급 경보(hazardous crisis)가 발생한 뒤 **운영자가 이를 처리(해소)하기까지의 수명주기**를 판정 가능한 계약으로 고정한다. 이 단위는 세 가지를 보장한다.

1. **표기 계약**: 사용자 대면 표면(이력 탭/네비게이션/페이지 제목)의 표기는 **"경보"**(알림/경보)다. "사고"가 아니다. 이는 **UI 카피 계약**일 뿐이며 내부 엔티티명·API 경로·DB 테이블·WS 타입은 모두 `incident` 계열을 유지한다.
2. **상태머신 계약**: 처리 상태 집합은 **`{open, resolved}`** 두 값뿐이다. 중간 확인 상태(`acknowledged`)와 확인(acknowledge) 액션/엔드포인트는 계약에서 제거된다. `open` 경보는 확인 단계 없이 **직접 해소**된다.
3. **해제 부작용 보존**: 경보 해소는 여전히 (a) 아카이브 finalize, (b) hw-gateway/센서 동기화, (c) WS `incident_resolved` 브로드캐스트를 수반하며, 센서 물리 버튼 해소 경로도 그대로 `open → resolved`로 동작한다.

배경 의도: 현장 운영에서 "확인 후 해소" 2단계는 실사용되지 않아 제거하고, 해소 시 사유(note) 입력을 **선택**으로 두어 즉시 해소를 마찰 없이 만든다. 동시에 사용자가 마주하는 어휘를 "사고(incident)"에서 "경보(alarm/alert)"로 통일해, 실제로 사고가 아닌 조기 경보 상황에서의 표현 오해를 없앤다.

## 언어 · 런타임

- **서버**: Go (표준 `net/http`, Go 1.22+ 메서드 라우팅), 포트 `:8080` — web-backend
- **클라이언트**: TypeScript + React (Vite), 브라우저 `fetch` / `WebSocket` — web-frontend
- **직렬화**: JSON (`Content-Type: application/json`), WS는 JSON 텍스트 프레임

## 의존 도구 · 시스템

- **SQLite** — `incidents` 테이블(상태·해제 attribution 영속). 내부 테이블·컬럼명은 `incident` 계열 유지.
- **JWT (HS256)** — `/api/incidents*` 접근 인증(user 조회, admin 해소). role: `admin` · `user` · `temp`(read-only).
- **WebSocket `/ws`** — 해소 브로드캐스트(`incident_resolved`) 소비.
- **하위 서비스**: recording(아카이브 finalize 비동기 트리거), hw-gateway(`/api/alert/resolved` 경유 MQTT 발행), 센서 펌웨어(물리 버튼 해소 → hw-gateway → `resolve-from-sensor`).

## 입력

내부 엔티티명·경로·WS 타입은 **불변**(리네임 대상 아님):

| Method | Path | Auth | 입력 |
|--------|------|------|------|
| GET | `/api/incidents` | user | query: `page`(≥1, def 1) · `limit`(def 20, max 100) · `from` · `to`(occurred_at, SQLite datetime) · `status`(**`open\|resolved`** — `acknowledged` 제거) |
| GET | `/api/incidents/active` | user | — (미해결 경보 배너 backfill 전용) |
| PATCH | `/api/incidents/{id}/resolve` | admin | `{resolutionNotes?: string}` — **선택**(생략·빈 문자열·공백 허용) |
| POST | `/api/incidents/{id}/resolve-from-sensor` | none (internal) | 센서 버튼 해소 — `interface-web-api.md` 계약 13 바디 그대로 |
| POST | `/api/incidents` | none (internal) | crisis 생성 — `interface-web-api.md` 계약 13 그대로(`open`으로 생성) |

**제거된 입력(계약에 존재하지 않음)**: `PATCH /api/incidents/{id}/acknowledge`. 확인 전이를 트리거하는 어떤 경로도 계약에 없다.

## 출력 (계약)

### `GET /api/incidents` → `200`

```json
{
  "data": [{
    "id": 12, "siteId": "site-001", "description": "gas leak",
    "occurredAt": "2026-04-13 10:20:30",
    "confirmedAt": null, "confirmedBy": null, "isTest": false,
    "status": "open | resolved",
    "resolvedAt": null, "resolvedBy": null, "resolutionNotes": null,
    "resolvedByKind": null, "resolvedById": null, "resolvedByLabel": null
  }],
  "pagination": { "page": 1, "limit": 20, "total": 57 }
}
```

- `status`는 **`"open"` 또는 `"resolved"`만** 취한다 — 응답 어디에도 `"acknowledged"` 값이 나타나지 않는다.
- `resolutionNotes`는 `null` 또는 빈 문자열일 수 있다(해소 시 노트를 주지 않았거나 빈 노트로 해소한 경우).
- `confirmedAt`/`confirmedBy`는 acknowledge 제거 후 **항상 `null`인 레거시 호환 필드**다 — 스키마·컬럼은 존치하되 쓰기 경로가 없다.

### `GET /api/incidents/active` → `200`

미해결 경보 배열. **미해결 = `status == "open"`만**(resolved 제외; `acknowledged`는 존재하지 않음). 각 원소는 WS `crisis_alert` payload(계약 14)와 **동형**이다(배너 backfill이 실시간 push와 같은 모양을 재구성):

```json
[{
  "incidentId": 12,
  "siteId": "site-001",
  "description": "gas leak",
  "occurredAt": "2026-04-13 10:20:30",
  "isTest": false,
  "status": "open",
  "site": { "address": "...", "managerName": "...", "managerPhone": "010-1234-5678" }
}]
```

- 모든 원소의 `status == "open"`. 발생시각 내림차순. 식별자는 `incidentId`, 현장 연락정보는 `site`로 중첩.

### `PATCH /api/incidents/{id}/resolve` → `200`

```json
{ "status": "resolved", "resolvedByKind": "web", "resolvedById": "<username>", "resolvedByLabel": "..." }
```

- `open` 경보를 **선행 확인 없이** 직접 해소한다.
- `resolutionNotes`가 생략·빈 문자열·공백뿐이어도 **`400`이 아니다** — `200`으로 해소되며 노트는 빈 값(또는 `null`)으로 저장된다.
- 이미 `resolved`면 `409`(재해소 방어, 종단 상태).

### `POST /api/incidents/{id}/resolve-from-sensor` → `200`

센서 물리 버튼 해소. `interface-web-api.md` 계약 13의 출력 계약을 그대로 유지한다(`resolvedByKind == "sensor_button"`). `open → resolved`로 전이하며, 매칭되는 미해결(=`open`) 경보가 없으면 `404`, 명시적 id가 이미 resolved면 `409`.

### 사용자 대면 표면 (web-frontend) — 스코프에 포함

이 단위의 스코프는 web-backend + **web-frontend**를 포함한다. 프론트 목표상태:

- 경보 이력 탭/네비게이션/페이지 제목에 렌더되는 텍스트는 **"경보"**를 포함하고 **"사고"를 포함하지 않는다**.
- 해소된 경보 카드는 `resolvedByKind`/`resolvedByLabel`로 해제 주체를 표시한다(web=🖥, sensor_button=🔘) — 표기 어휘만 "경보"로 통일되며 attribution 표시는 유지된다.
- **acknowledge 미노출**: 경보 이력 화면과 `status` 필터에 "확인(acknowledge)" 버튼·필터 옵션·상태 라벨이 없다 — 미해결 경보의 처리 액션은 "해소"뿐이다.
- **해제 노트 선택**: ResolveModal에는 노트 필수검증·"(필수)" 라벨이 없어 **빈 노트로도 제출 가능**하다.

### 델타 (목표상태로의 변경분 — acknowledged/"사고" 제거 지시는 이 섹션에 집약)

> **편집 경계 (SSOT 위임)**: 본 잎 워크트리는 **코드 + 자기 `tests/` + 본 스펙 문서만** 편집한다. `interface-web-api.md` 계약 2 정합화(빈 노트 `400`→`200`, `PATCH .../acknowledge` 및 `acknowledged` 상태 제거, `/active` 집합 `{open,acknowledged}`→`{open}`)는 **오케스트레이터가 머지 시점에 단독 반영**한다 — 본 잎은 `interface-web-api.md`를 편집하지 않는다.

아래는 이 단위가 목표상태(`{open, resolved}` 두 상태·노트 선택·"경보" 표기)에 도달하기 위한 변경분이다. 본문·단언은 목표상태(positive)로 기술하고, 제거 개념은 여기에 모은다.

**상태집합·엔드포인트 (acknowledged 제거) — 코드+본 스펙**
- **레거시 행 마이그레이션 (배포 계약)**: 배포 시 기존 `status='acknowledged'` 행은 `open`으로 승격한다(진행 중=미해소 보존). 배포 후 **어떤 시점에도** `acknowledged` 값 행은 DB·API 응답에 존재하지 않음을 게이트로 강제한다(단언 M).
- **엔드포인트 제거**: `PATCH /api/incidents/{id}/acknowledge` 라우트·핸들러·성공 응답을 제거한다. 확인 전이를 유발하는 경로가 없다.
- **enum 축소**: `status` 값 집합 `{open, acknowledged, resolved}` → **`{open, resolved}`**. 목록 필터·응답 스키마·`/active` 집합에서 `acknowledged`를 제거한다.
- **status 필터 화이트리스트**: `GET /api/incidents?status=`는 `{open, resolved}`만 수용하고, 그 외 값(예 `?status=acknowledged`)은 **`400`**으로 거절한다(단언 N).

**센서 해소 폴백 술어 정정 — 코드**
- resolve-from-sensor 폴백 매칭 및 재해소 방어 술어를 `status != 'resolved'` → **`status = 'open'`**으로 정정한다. 마이그레이션(C1)으로 `acknowledged`가 소거된 뒤 폴백은 `open`만을 대상으로 매칭한다(순서의존은 단언 I 참조).

**해제 노트 선택화 — 코드+본 스펙**
- `PATCH .../resolve` 입력 `{resolutionNotes*: non-empty string}` → **`{resolutionNotes?: string}`**. 노트 부재·빈 문자열·공백은 `400`이 아니라 `200`이며 빈 값(또는 `null`)으로 저장된다(단언 B).

**표기 리네임 (web-frontend 카피) — 비가역 호환 가드레일 유지**
- 사용자 대면 텍스트를 "사고"→"경보"로 통일한다. **내부 엔티티명·경로·DB 테이블·WS 타입(`crisis_alert`/`incident_resolved`)은 변경 금지** — 리네임은 web-frontend 표기 카피에 한정한다.

**오케스트레이터 머지 반영분 (본 잎 편집 대상 아님) — `interface-web-api.md` 계약 2/13/14**
- 계약 2에서 `PATCH .../acknowledge` 행·출력·단언 삭제, `status` enum `{open, acknowledged, resolved}`→`{open, resolved}`, `/api/incidents/active` 집합 `{open, acknowledged}`→`{open}`, 상태기계 문구 `open→acknowledged→resolved`→`open→resolved`(중간 확인 상태 없음), `PATCH .../resolve` 입력 non-empty→optional 및 "notes 비어있으면 `400`" 단언 삭제.

## 핵심 로직 (동작)

- **상태 기계**: `open → resolved` 단일 전이. `resolved`는 종단(재해소 `409`). 중간 상태 없음 — 시스템에 `acknowledged` 상태 값도, 그 전이를 유발하는 액션도 존재하지 않는다.
- **레거시 상태 마이그레이션**: 배포 시 기존 `acknowledged` 행은 `open`으로 승격되며(진행 중=미해소 보존), 배포 후 어떤 시점에도 `acknowledged` 값 행은 DB·API에 존재하지 않는다(단언 M 게이트). 이 소거가 완료된 뒤에야 센서 폴백(`status='open'` 술어)이 open만을 대상으로 올바르게 매칭한다.
- **직접 해소**: `open` 경보는 선행 확인 없이 해소 가능. 확인 단계를 거치도록 강제하지 않는다.
- **해제 노트는 선택 메타데이터**: 노트 유무·공백 여부는 해소 성공/실패에 영향을 주지 않는다. 빈 노트로 해소해도 `resolved` 상태가 확정된다.
- **양방향 해소 attribution 유지**: `resolvedByKind ∈ {"web", "sensor_button", null}`. 웹 해제(본 계약 `resolve`)와 센서 버튼 해제(`resolve-from-sensor`)가 동일 필드에 기록된다.
- **해소 부작용 3종(보존)**: 해소 성공 시 — (a) recording 아카이브 finalize 비동기 트리거, (b) hw-gateway `/api/alert/resolved`(계약 15) 경유 MQTT `safety/{siteId}/alert/resolved` 발행, (c) WS `incident_resolved`(계약 14) 브로드캐스트. 웹 경로·센서 경로 모두 (a)(c)를 수반한다.
- **미해결 = `open`**: "미해결/active"의 정의는 `status == "open"`. `/api/incidents/active`와 배너 backfill, 그리고 센서 해소 폴백 체인의 "가장 최근 미해결 incident" 매칭은 모두 `open` 집합을 대상으로 한다.
- **표기 리네임의 경계**: "경보"로의 어휘 변경은 web-frontend가 렌더하는 사용자 대면 텍스트에 한정된다. API 응답 필드명, 경로, WS 메시지 타입, DB 스키마는 `incident` 어휘를 유지하여 계약·소비자 호환성을 깨지 않는다.

## 검증 단언 (TDD)

- **A (핵심)** — `open` 경보에 `PATCH /api/incidents/{id}/resolve`(유효 노트) → `200`, 응답 `status == "resolved"` · `resolvedByKind == "web"`. 선행 acknowledge 호출 없이 성공한다.
  ```bash
  curl -s -X PATCH -H "Authorization: Bearer $ADMIN" \
    http://localhost:8080/api/incidents/$ID/resolve \
    -d '{"resolutionNotes":"현장 확인 완료"}' | jq -e '.status=="resolved" and .resolvedByKind=="web"'
  ```
- **B (핵심, mutating·terminal)** — 해제 노트는 선택: 다음 본문 변형이 **모두 `400`이 아니라 `200`**이고 저장 상태 `resolved`, 노트는 빈 값(또는 `null`)으로 저장된다 — ① 요청 본문 부재, ② 빈 본문, ③ `{}`, ④ `resolutionNotes` 필드 생략, ⑤ `{"resolutionNotes":""}`, ⑥ `{"resolutionNotes":"   "}`. `resolve`는 종단(재해소 `409`)이므로 **각 변형마다 새 `open` incident를 시딩**해 그 id로 1회 호출한다(변형 간 incident 공유 금지).
  ```bash
  # 각 변형마다 격리 스택에 새 open incident 시딩(내부 POST /api/incidents) → 그 id에 1회 resolve → 200 기대
  for body in '' '{}' '{"resolutionNotes":""}' '{"resolutionNotes":"   "}'; do
    ID=$(seed_open_incident)   # 케이스별 신선한 open incident
    curl -s -o /dev/null -w '%{http_code}\n' -X PATCH -H "Authorization: Bearer $ADMIN" \
      http://localhost:8080/api/incidents/$ID/resolve ${body:+-d "$body"}   # → 200 (본문 부재 = -d 없음)
  done
  ```
- **C (핵심)** — 계약에 **acknowledge 전이가 없다**: `PATCH /api/incidents/{id}/acknowledge` 시도는 계약된 성공 응답(`200 {"status":"acknowledged"}`)을 반환하지 않는다(라우트 부재로 `404`/`405`). 그리고 어떤 `GET /api/incidents` 응답에도 `status == "acknowledged"`인 원소가 없다.
  ```bash
  curl -s -o /dev/null -w '%{http_code}' -X PATCH -H "Authorization: Bearer $ADMIN" \
    http://localhost:8080/api/incidents/$ID/acknowledge   # → 404 또는 405 (200 아님)
  ```
  UI에서 acknowledge 버튼·필터·상태 라벨이 미노출인지는 API로 판정 불가하므로 별도 needs-browser 단언(O)이 커버한다. 본 단언 C는 라우트 부재(`404`/`405`)와 응답 내 `acknowledged` 값 부재만 API로 판정한다.
- **D (핵심)** — `GET /api/incidents/active` → `200`, 모든 원소 `status == "open"`(resolved·acknowledged 미포함), 각 원소에 `incidentId` · `site.address` · `site.managerName` · `site.managerPhone` 존재.
  ```bash
  curl -s -H "Authorization: Bearer $T" http://localhost:8080/api/incidents/active \
    | jq -e 'all(.[]; .status=="open" and has("incidentId") and (.site|has("address") and has("managerName") and has("managerPhone")))'
  ```
  판정은 **마이그레이션이 적용된 DB**를 대상으로 한다(레거시 `acknowledged` 소거 후). 빈 테스트 DB에서의 vacuous OK를 피하려면 최소 1개의 open incident를 시딩한다.
- **E** — `GET /api/incidents` 응답의 모든 `data[].status`가 `{open, resolved}` 안에 든다(`acknowledged` 부재).
  ```bash
  curl -s -H "Authorization: Bearer $T" 'http://localhost:8080/api/incidents?limit=100' \
    | jq -e 'all(.data[]; .status=="open" or .status=="resolved")'
  ```
  판정은 **마이그레이션이 적용된 DB**를 대상으로 한다. 빈 테스트 DB에서의 vacuous OK를 피하려면 최소 1개의 incident를 시딩한다.
- **F** — `GET /api/incidents?status=resolved` → `data[]`의 모든 `status == "resolved"`. `status=open` → 모든 `status == "open"`.
- **G (핵심)** — 같은 경보에 `resolve` 재호출 → `409`(resolved는 종단).
- **H (핵심)** — 해소 성공 직후 접속 중 WS 클라이언트가 `type == "incident_resolved"` 메시지를 수신하고 `payload.incidentId`가 해소한 경보의 id와 일치(계약 14 교차).
- **I (핵심, mutating·terminal)** — 센서 버튼 해소 경로 유지: `open` 경보에 대한 `POST /api/incidents/{id}/resolve-from-sensor`(유효 바디) → `200`, `resolvedByKind == "sensor_button"`, 이후 그 경보의 `status == "resolved"`. `open → resolved`로 직접 전이한다(중간 확인 없음). **순서의존**: C1 마이그레이션 후(`acknowledged` 0건 상태)에 폴백 매칭(`status='open'` 술어)이 `open`만을 대상으로 함을 전제로 판정한다.
- **J** — 해소 부작용 관측: 웹 해소 성공 시 (a) 해당 site로 hw-gateway `/api/alert/resolved` 발행(MQTT `safety/{siteId}/alert/resolved` 관측 가능) (c) WS `incident_resolved` 브로드캐스트가 각각 1회 발생. (아카이브 finalize (b)는 비동기 트리거이므로 recording 접면에서 별도 관측.)
- **K (핵심)** — 사용자 대면 표기: web-frontend의 경보 이력 탭/네비/페이지 제목 렌더 텍스트가 문자열 **"경보"**를 포함하고 **"사고"를 포함하지 않는다**(브라우저에서 관측 가능한 UI 텍스트 단언 — 예: 이력 탭 라벨의 textContent에 "경보" 포함, "사고" 미포함).
- **L** — user 토큰으로 `PATCH /api/incidents/{id}/resolve` → `403`(해소는 admin 전용; 조회 `GET /api/incidents`는 user `200`).
- **M (핵심)** — 레거시 마이그레이션 게이트: 배포/마이그레이션 적용 후 `GET /api/incidents?limit=100` 응답에 `status == "acknowledged"`인 행이 **0건**이다(기존 `acknowledged` 행은 `open`으로 승격됨). 최소 1개 시드가 존재하는 DB에서 판정(빈 테스트 DB의 vacuous OK 방지).
  ```bash
  curl -s -H "Authorization: Bearer $T" 'http://localhost:8080/api/incidents?limit=100' \
    | jq -e '([.data[] | select(.status=="acknowledged")] | length) == 0 and (.data | length) >= 1'
  ```
- **N** — status 필터 화이트리스트: `GET /api/incidents?status=acknowledged`(및 `{open,resolved}` 밖의 임의 값) → `400`. `?status=open`·`?status=resolved`는 `200`.
  ```bash
  curl -s -o /dev/null -w '%{http_code}' -H "Authorization: Bearer $T" \
    'http://localhost:8080/api/incidents?status=acknowledged'   # → 400
  ```
- **O (핵심, needs-browser SKIP — 의도적)** — 프론트 acknowledge 제거 + 노트 선택화: web-frontend 경보 이력 화면·`status` 필터에 "확인(acknowledge)" 버튼·필터 옵션·상태 라벨이 **렌더되지 않으며**, ResolveModal에 "(필수)" 라벨·노트 필수검증이 없어 **빈 노트 제출이 가능**하다. 브라우저 관측이 필요하므로 K와 동일하게 **needs-browser SKIP(의도적)**으로 선언한다(API 계층만으로 판정 불가 — 라우트 부재는 단언 C가, 상태집합 부재는 E/M이 API 측면을 커버).

## 검증 스킵 선언 (선택)

- **K** — 사유: 브라우저 렌더 텍스트 단언으로 Playwright 세션이 필요(web-frontend needs-browser 계열). API 계층만으로는 판정 불가. · 중요도: **핵심(load-bearing)** (표기 리네임이 이 단위의 3대 보장 중 하나) · 해소 조건: Playwright 세션 실행 시 즉시 판정(INDEX.md SKIP 해제 조건 4와 동류).
- **O** — 사유: 브라우저 렌더 관측 필요(web-frontend needs-browser). acknowledge UI 미노출·ResolveModal 노트 선택화는 API로 판정 불가. · 중요도: **핵심(load-bearing)** · 해소 조건: Playwright 세션 실행 시 즉시 판정(K와 동류).
- **A · B · G · I** — 사유: mutating·terminal 단언 — 경보를 `resolved`로 종단 전이시키거나 재해소 `409`를 유발하므로 **격리 스택 + `ADMIN_TOKEN` + 케이스별 새 `open` incident 시딩**이 필요하다(공유 DB에 부작용 잔존). · 중요도: **핵심** · 기본 **SKIP** 대상이며 격리 스택이 준비되면 판정한다.
