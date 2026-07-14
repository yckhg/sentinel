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

### ★ 핵심(load-bearing) 스킵 — ✅ 전부 판정 완료(2026-07-14), 아래 이력 보존

> **UPDATE 2026-07-14:** 이 절이 "사람 승인 대상·판정 불가"로 남겼던 핵심 SKIP 10개를 실제로 판정했고 **전부 통과(10/10 OK)**. 정찰 결과 sensor 8개(A2·D·E·E2·F·F2·G·H1)의 "격리 compose 스택 필요" 분류는 **과보수적**이었음 — 핸들러 팩토리 함수 직접 호출·admin context 주입·`BroadcastDeviceReappeared`/`sendReappearedSnapshot` func-var 시밍으로 **compose 스택 없이 in-process Go 상시 테스트**로 판정 가능. 라이브 스택이 실제로 필요한 건 B2(recording+ffmpeg)·K(Playwright)뿐. 3갈래 병렬 구현 → **독립 검증자 2개**가 구현자 보고 불신·재실행 + **뮤테이션으로 비공허성 실증**(소스에 회귀 주입 시 각 테스트 red 확인) + 소스 무변경 감사 + 운영 `sentinel-*` 무접촉 증명. 커밋 `19db723`(main, push 미실행): `sensor_device_lifecycle_test.go`(8단언)·`archive_evidence_preservation_test.go`(B2)·`verify/device-mgmt/`(devverify compose + Playwright). 아래 표는 당시 SKIP 사유의 **이력**으로 보존하되, "판정 결과" 열이 현재 상태다.

| 단언ID | 잎 | 당시 SKIP 사유(이력) | 판정 결과(2026-07-14) |
|--------|-----|------|------|
| **B2** | camera | mutating · 격리 compose 스택. 카메라 DELETE 후 아카이브(MP4+metadata) 잔존을 능동 조회로 판정하려면 별도 DB 볼륨·유효 미디어 픽스처·라이브 recorder 필요. | ✅ **OK** — `services/recording/archive_evidence_preservation_test.go`. 실 protect→finalize(ffmpeg concat)→실 `fetchCameras`+`RecordingManager.Reload`(removed 분기, recorder 정지) 재현, finalize 직후·reconcile 후 MP4/metadata 존재 이중 확인(비공허). 뮤테이션: reconcile가 아카이브 purge 시 (b)(c) red. |
| **A2·D·E·E2·F·F2·G·H1** | sensor | mutating · 격리 compose 스택 필요로 판단(자동발견 D·sticky 삭제 E·E2·WS 통지 F·F2·위기 자동등록 H1·health A2·재출현 G). | ✅ **OK (분류 정정)** — 격리 스택 불필요. `sensor_device_lifecycle_test.go` **in-process 상시 테스트**로 판정. 각 테스트 뮤테이션으로 비공허 실증(silent-revive 재주입→E·E2 red, once-only 가드 제거→F got 2, null 제거→A2 합 불변식 red 등). |
| **K** | sensor | needs-browser (Playwright). 프론트 `device_reappeared` 재활성 UI, 재출현을 `/api/devices/all` 폴링으로 표면화(WS 실시간 아님). | ✅ **OK** — `verify/device-mgmt/` devverify 격리 스택 + Playwright. 추가→삭제(sticky)→POST seen→폴링 대기→재출현 패널·원클릭 재활성 검증, 운영 무접촉 증명. 뮤테이션: seen 재신호 제거 시 패널 미출현. **폴링↔실시간 WS 절충은 사람 결정 = 폴링 수용 확정.** |

> **결론(갱신됨)**: 위 핵심 SKIP은 더 이상 "사람 승인 대상"이 아니다 — 전부 판정·통과했다. 유일하게 남았던 순수 사람 결정(K의 폴링 대체 수용 여부)은 **수용으로 확정**됐다. 방법론적 교훈: "mutating이라 격리 스택 필요"라는 SKIP 분류는 in-process 시밍 가능성을 먼저 타진해야 한다(8/10이 정찰에서 값싼 in-process로 재분류됨).

### 비-핵심 스킵 / 수용된 nice-to-have
- 없음(상시 게이트로 승격 가능했던 항목 I·I2·once-only·zero-write는 모두 R3에서 상시화 완료).

---

## 교차단위 회귀 주의
- **공유 파일**: `services/web-backend/` 라우팅(main.go)은 camera=cameras.go만, sensor=devices.go/incidents.go/main.go 1줄로 봉투 분리 → 충돌 낮음. 테스트 심볼 충돌 1건(funcBody)은 개명 해소.
- **접면 SSOT 문서**: 이중 편집 회피 위해 sensor 머지 후 일괄 반영. `interface-web-api.md` 계약 6·13·14의 자동복원 계약이 sticky로 전면 플립됐으므로, 이후 이 문서를 참조하는 다른 잎/세션은 **신 sticky 계약**을 정본으로 삼아야 한다(구 자동복원 서술 잔재 0 — 독립 검증 확인).
- **hw-gateway `X-Internal-Token`**: seen·incidents 두 경로 한정. 다른 internal 경로(카메라 reload/list, links/temp 등)의 앱레벨 차단은 **시스템 차원 후속 과제**로 남음(과대주장 없음 — interface-web-api.md 알려진 갭 item 3에 명시).

## 미해결 / 블록 항목
- 없음(블록 없음). 두 잎 모두 SPEC-OK → 구현 → 독립 검증 CRITICAL/HIGH 0 → 통합 머지 → SSOT 정합 완료.
- ~~잔여 판정은 위 **핵심 스킵**(격리 스택·브라우저 하네스 준비 시)뿐.~~ → **해소(2026-07-14): 핵심 스킵 10개 전부 판정·통과.** 잔여 미해결 없음.

## 결론
- 모든 작업 단위 **DONE**, 통합 브랜치 `spec/device-mgmt`에 누적 머지 완료(main 반영).
- 통합 최종 sha: **8eb03d3**. 격리 컨테이너 통합 build/vet/test -race green(문서-only 마지막 변경은 코드 무영향).
- **UPDATE 2026-07-14:** 핵심 스킵(camera B2 / sensor A2·D·E·E2·F·F2·G·H1·K) **10개 전부 판정 완료, 10/10 OK**(독립 검증자 2개 뮤테이션 비공허 실증). 커밋 `19db723`(main, push는 사람이 결정 — 미실행). K 폴링↔실시간 WS 절충은 **폴링 수용 확정**. 상세는 위 SKIPPED 절 UPDATE 참조.
