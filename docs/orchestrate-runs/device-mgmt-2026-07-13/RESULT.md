# RESULT — 장치(카메라·센서) 관리 2잎 (spec-orchestrate 무인 실행)

통합 브랜치 `spec/device-mgmt` 최종 HEAD: **8eb03d3**
**main 머지·git push는 사람이 결정한다** (오케스트레이터는 main 무접촉·push 금지 준수).

두 작업 단위는 독립이라 병렬 진행했고, 공유 핫스팟(web-backend·접면 SSOT 문서)은 **머지 순서 직렬화 + 봉투 스코프 분리**로 충돌을 회피했다(camera=cameras.go/recording, sensor=devices.go/incidents.go/migrations; SSOT 문서는 sensor 머지 후 일괄 반영).

---

## 작업 단위별 결과

### 1) camera-change-propagation — DONE
- **구현**: `triggerRecordingReload`를 카메라 CRUD 3핸들러(create·update·delete)에 배선 → DB 쓰기 커밋 **후** 3소비자(cctv-adapter·youtube-adapter·recording)로 비동기·최선노력 팬아웃. recording은 reconcile로 재조정하되 삭제 key recorder 정지 시 `archiveManager`·`metadata.json`·아카이브 파일 **무접촉**(증거 보존 불변식). impl HEAD 8cbd3a0, 통합 머지 f1a88cb.
- **스펙 게이트(STEP 0.5)**: spec-cross-validate R1(CRITICAL 검출)→R2(HIGH 정정)→**R3 전 렌즈 HIGH0 → SPEC-OK (21a3a9b)**.
- **독립 검증(STEP D, 셀프검증 금지)**: R1·R2 비공허성 능동 입증 → R3 정적 순서 가드 하드닝. **CRITICAL/HIGH 0 전 라운드.**
- **테스트**: A(in-process 3소비자, 8서브) green · B1s(정적) green · 정적 순서 게이트 green. `go build/vet/test -race` clean.
- **SKIPPED**: B2 (아래 핵심 스킵).

### 2) sensor-device-lifecycle — DONE
- **구현**: migration v21(devices 테이블 재구축 → `last_seen` nullable) + v22(`reappear_alerted_at`), `POST /api/devices`(admin 생성-또는-재활성 201/409/200), sticky 삭제(seen·incidents 양경로에서 `deleted_at=NULL` 제거), `maybeAlertReappear` rowcount-가드 공유 헬퍼(정확히 1회 + 재연결 backfill), `X-Internal-Token` fail-closed(seen·incidents), `/restore` 제거, null last_seen→offline health 특례, WS `device_reappeared` broadcast, 프론트. impl HEAD 06e121f, 통합 머지 eb91d0f.
- **스펙 게이트(STEP 0.5)**: R1(CRITICAL)→R2(HIGH)→R3(risk HIGH: hw-gateway 헤더 폭발반경)→**R4 3렌즈 HIGH0 → SPEC-OK (6860a72)**.
- **독립 검증(STEP D, 셀프검증 금지, 강제-5×~5R)**: R1(상시 9단언 비공허 OK)→R2(load-bearing 주장 RED 위임 확인)→**R3: once-only 상시 게이트化 + zero-write 게이트 + 핫경로 tx 제거 + alert_state 대칭**. **CRITICAL/HIGH 0.**
- **테스트**: A·B·C·C2·H2·I·I2·J·L·migration·once-only강화·zero-write passed (상시 11 PASS). `go build/vet/test -race` clean.
- **SKIPPED**: A2·D·E·E2·F·F2·G·H1·K (아래).

### 통합 + SSOT 정합
- **교차단위 충돌 해소**: 두 잎이 공유하는 테스트 심볼 `funcBody` redeclared → `funcBodyBySig` 개명 (f1d5ddb). 격리 컨테이너에서 통합 `build/vet/test -race` **green**.
- **compose**: web-backend·hw-gateway 두 서비스에 동일 `INTERNAL_TOKEN` 배선 (1fb7a54) — 누락 시 heartbeat seen·위기 자동등록이 전면 401되는 fail-closed 경계.
- **접면 SSOT 4문서 정합** (구현자 편집 → **독립 검증자 11항목 전원 OK**, 코드 정본 대조, 신·구 단언 상호배타 잔재 0):
  - `recording.md` (206570b): reconcile를 카메라 CRUD 3소비자 팬아웃 정식 소비자 + 증거보존 불변식으로.
  - `interface-web-api.md`·`web-backend.md`·`hw-gateway.md` (8eb03d3): sticky 삭제·재출현 경보·`X-Internal-Token` fail-closed·자동복원 계약/단언 전면 플립·`POST /api/devices` 신설·admin 게이트·nullable last_seen·`reappear_alerted_at`·health 특례.

---

## SKIPPED 목록 (초록으로 세지 않음 — 항상 표면화)

### ★ 핵심(load-bearing) 스킵 — 사람 승인 대상

| 단언ID | 잎 | 종류 | 사유 | 중요도 |
|--------|-----|------|------|--------|
| **B2** | camera | mutating · 격리 compose 스택 | 카메라 DELETE 후 recording reconcile 완료를 폴링 관측 → 삭제 stream_key 아카이브(MP4+metadata) **잔존을 능동 조회**로 판정. 별도 DB 볼륨·유효 미디어 픽스처·라이브 recorder가 필요해 상시 판정 불가. | 핵심 |
| **A2·D·E·E2·F·F2·G·H1** | sensor | mutating · 격리 compose 스택 | 자동발견 등록(D)·sticky 삭제 전이(E·E2)·WS 통지 관찰(F·F2)·위기 유입 자동등록(H1·E2)·health 집계(A2)·재출현(G)는 `devices`/WS에 부작용. **격리 스택 + admin JWT + `INTERNAL_TOKEN`** 하에서만 판정 가능. 상시(A·B·C·C2·H2·I·I2·L·J·migration)는 격리 web-backend + 정적 스캔으로 이미 green. | 핵심 |
| **K** | sensor | needs-browser (Playwright) | 프론트 `device_reappeared` 재활성 UI. 백엔드 WS broadcast+backfill은 완비이나, 프론트는 app-root 단일 소켓 스코프 밖이라 **`/api/devices/all` 폴링으로 표면화**(WS 실시간 아님). 실시간 WS 표면화·브라우저 E2E 판정은 needs-browser. | 핵심 |

> **사람 승인 필요 이유**: 위 스킵들은 "커버됐다"로 오인되면 안 되는 load-bearing 단언이다. mutating 격리 compose 스택(`docker compose -p devverify`, 별도 DB 볼륨·mock 수신기, 운영 `sentinel-*` 무접촉) 구성과 Playwright 브라우저 하네스가 준비되면 판정 가능하다. 특히 **sensor K의 프론트 폴링 대체**는 설계상 실시간 WS 이탈이므로(검증 MEDIUM-1로도 표면화), 이 UX 절충을 수용할지 사람이 결정해야 한다.

### 비-핵심 스킵 / 수용된 nice-to-have
- 없음(상시 게이트로 승격 가능했던 항목 I·I2·once-only·zero-write는 모두 R3에서 상시화 완료).

---

## 교차단위 회귀 주의
- **공유 파일**: `services/web-backend/` 라우팅(main.go)은 camera=cameras.go만, sensor=devices.go/incidents.go/main.go 1줄로 봉투 분리 → 충돌 낮음. 테스트 심볼 충돌 1건(funcBody)은 개명 해소.
- **접면 SSOT 문서**: 이중 편집 회피 위해 sensor 머지 후 일괄 반영. `interface-web-api.md` 계약 6·13·14의 자동복원 계약이 sticky로 전면 플립됐으므로, 이후 이 문서를 참조하는 다른 잎/세션은 **신 sticky 계약**을 정본으로 삼아야 한다(구 자동복원 서술 잔재 0 — 독립 검증 확인).
- **hw-gateway `X-Internal-Token`**: seen·incidents 두 경로 한정. 다른 internal 경로(카메라 reload/list, links/temp 등)의 앱레벨 차단은 **시스템 차원 후속 과제**로 남음(과대주장 없음 — interface-web-api.md 알려진 갭 item 3에 명시).

## 미해결 / 블록 항목
- 없음(블록 없음). 두 잎 모두 SPEC-OK → 구현 → 독립 검증 CRITICAL/HIGH 0 → 통합 머지 → SSOT 정합 완료.
- 잔여 판정은 위 **핵심 스킵**(격리 스택·브라우저 하네스 준비 시)뿐.

## 결론
- 모든 작업 단위 **DONE**, 통합 브랜치 `spec/device-mgmt`에 누적 머지 완료.
- 통합 최종 sha: **8eb03d3**. 격리 컨테이너 통합 build/vet/test -race green(문서-only 마지막 변경은 코드 무영향).
- **main 머지·git push는 사람이 결정.** 핵심 스킵(camera B2 / sensor A2·D·E·E2·F·F2·G·H1·K)은 사람 승인 대상.
