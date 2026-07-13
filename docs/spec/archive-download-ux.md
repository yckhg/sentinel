# 아카이브 보관(archive) → 다운로드 UX 스펙

> 상태: 살아있는 계약 (living spec) · 독자: 스펙 작성자 / 오케스트레이터

## 목적 / 의도

사용자가 `/cctv` 화면에서 특정 구간을 **보관(archive)** 요청하면, 그 아카이브가 **언제 다운로드 가능한 상태가 되는지**를 사용자가 관측할 수 있어야 한다. 이 스펙은 다음을 보장한다.

- 아카이브는 관측 가능한 **상태 수명주기**를 가지며, 완료 시점에 **완료 타임스탬프**를 획득한다.
- 다운로드 제공(affordance)은 **완료 상태에만 게이트**된다 — 준비되지 않은 아카이브를 다운로드하려 해도 깨진 파일이 조용히 내려오지 않는다.
- UI는 요청 이후의 **진행 상태를 실시간으로 표면화**하여, 사용자가 "요청됨/처리 중"에서 "준비됨"으로의 전이를 헤매지 않고 본다.
- **실패**는 사유와 함께 드러나며 조용히 사라지지 않는다.

이 계약은 "요청은 했는데 언제 받을 수 있는지 알 수 없다"는 UX 결함을 계약 수준에서 제거하기 위해 존재한다.

## 언어 · 런타임

- 아카이브 상태·수명주기·다운로드 게이트: 아카이브를 생성·병합·서빙하는 백엔드 서비스(Go 런타임, 파일시스템 기반 아카이브 저장소).
- 다운로드 UX: 모바일 우선 React SPA(브라우저 런타임), nginx proxy 경유로 백엔드 REST를 호출.

(식별용이며 빌드/구현 지시가 아니다.)

## 의존 도구 · 시스템

- 아카이브 목록·상태·다운로드 REST 접면 — SSOT: `docs/interfaces/web-api.md` §8, `docs/spec/interface-web-api.md`.
- 아카이브 status enum 값 정의 — SSOT: recording 서비스 스펙(`docs/spec/recording.md`).
- 영구 아카이브 미디어(MP4) 저장소 및 상태 조회 경로.

## 계약 대상 분할

이 스펙은 하나의 UX 의도를 두 산출물 단위로 나눈다. 각 단위는 자기 입력·출력·검증 단언을 가지며, 둘 사이의 주고받음은 아래 **(횡단) 접합부**가 소유한다.

- **단위 A — 아카이브 수명주기 · 다운로드 게이트 (제공자 측):** 아카이브의 상태 전이, 완료 타임스탬프, 다운로드 게이트를 보장한다.
- **단위 B — 다운로드 UX (소비자 측):** 상태를 관측해 다운로드 affordance 게이팅, 진행 피드백, 목록 노출, 실패 표면화를 보장한다.

---

## 단위 A — 아카이브 수명주기 · 다운로드 게이트 (제공자 측)

### 입력

- 보관 요청(임의 구간 즉시 아카이브, 또는 incident 구간 아카이브).
- 아카이브 목록 조회 요청.
- 특정 아카이브의 다운로드 요청(아카이브 식별자 기준).

### 출력 (계약)

- 아카이브 목록의 각 항목은 `status` 필드를 가지며, 값은 정확히 6종 `{protecting, pending, finalizing, processing, completed, failed}` 중 하나다. 이 중 **종단(terminal) 상태는 `completed`와 `failed`**이고, 나머지 4종은 **미완료(in-progress)** 상태다.
- 아카이브가 `completed`에 도달하면 항목은 **완료 타임스탬프 `completedAt`**(RFC3339, UTC 고정)을 가진다. 미완료·`failed` 상태에서는 `completedAt`이 null이거나 부재한다. (프론트는 이 UTC 값을 로컬 시각으로 변환해 표시한다 — 값의 정본은 UTC, 표시는 로컬.)
- `failed` 상태의 항목은 **실패 사유**를 담는 필드 `lastError`(recording.md의 `lastError`, P-2에서 non-empty 보장, 사람이 읽을 수 있는 문자열)를 가진다.
- 다운로드 응답:
  - 아카이브가 `completed`이면 다운로드는 **완결된 MP4 미디어**(2xx, `video/mp4`, 비어있지 않은 본문)를 반환한다.
  - 아카이브가 미완료(`protecting`/`pending`/`finalizing`/`processing`)이면 다운로드는 **미디어를 반환하지 않고 비-2xx(미준비 4xx, 예: 409)로 거절**한다. 부분/0바이트 파일을 2xx로 내려보내지 않는다.
  - `failed` 아카이브의 다운로드도 미디어를 반환하지 않는다(비-2xx, 409).

### 핵심 로직 (동작)

- 갓 요청된 아카이브는 **미완료 상태로 시작**한다(요청 즉시 `completed`로 나타나지 않는다).
- 상태는 미완료에서 종단(`completed` 또는 `failed`)으로 전이하며, **종단 상태는 이후 뒤집히지 않는다**(단조성). `completed`가 다시 미완료로 돌아가거나 `failed`가 `completed`로 바뀌지 않는다.
- `completed` 진입과 `completedAt` 설정, 그리고 다운로드 가능성은 **동시적 불변식**이다 — 셋 중 하나만 참인 중간 상태가 소비자에게 관측되지 않는다.
- 미완료 아카이브의 미디어는 다운로드로 조용히 노출되지 않는다(부분/0바이트 파일 금지).

### 검증 단언 (TDD)

> **판정 전제(vacuity 가드)**: 아래 `all(.[]; …)` 류 단언(A1·A3·A4·A7)은 아카이브 목록이 0개이면 공허하게(vacuously) 통과한다. 따라서 각 단언은 **최소 1개의 해당 대상 상태 아카이브가 존재하는 fixture**를 전제하며(A1은 임의 1개, A3은 `completed` 1개, A4는 `failed` 1개), 판정 스크립트는 `length > 0` 또는 대상 상태 항목 존재를 먼저 가드한 뒤 평가한다. 대상 항목이 하나도 없으면 OK가 아니라 **판정 불가(전제 미충족)**로 처리한다.

- **A1 (핵심)** — `GET /api/archives` 각 항목의 `status`는 6종 enum 중 하나다. enum 밖 값은 나타나지 않는다. (목록 non-empty 전제 — 위 vacuity 가드.)
- **A2 (핵심)** — 방금 생성 요청한 아카이브는 응답 목록에서 **not completed**(정확히 `completed`가 아닌 상태) 상태로 관측된다. 판정 기준은 "`status != "completed"`"로 못박으며(line 48의 미완료 4종 정의와 달리 `failed`도 이 기준에 포함) — 이는 다운로드 게이트가 `failed`까지 409로 거절하는 것(A8/출력계약)과 일관된다. · **전제(mutating 게이트)**: 이 단언은 방금 생성을 위한 **mutating 게이트(아카이브 생성 POST 필수, `ALLOW_MUTATING`/`SPEC_TDD_ALLOW_MUTATING`)** 에 의존한다 — vacuity 가드도 SKIP 선언도 없으므로, 게이트가 없어 갓 생성한 대상 항목을 확보할 수 없으면 OK가 아니라 **판정 불가/SKIP**로 처리한다.
- **A3 (핵심)** — `status == "completed"`인 항목은 non-null `completedAt`(RFC3339 파싱 가능, UTC)을 가진다. 미완료 항목은 `completedAt`이 null 또는 부재다. 값은 UTC 정본만 요구하며, 로컬 표시 변환은 소비자(단위 B) 책임이다.
  - **전제**: 이 단언은 `completedAt` 델타가 **recording SSOT+코드에 착지한 이후**에만 판정 가능하다(생산자=recording, 아래 "API 계약 델타" 참조). 델타 미착지 시 A3은 **SKIP**한다.
  - **레거시 처리**: A3은 델타 착지 **이후에 생성된 아카이브에 한정**한다(필드 도입 전 이미 `completed`가 된 레거시 아카이브는 판정 대상에서 제외 — mtime 백필하지 않는다).
- **A4 (핵심)** — `status == "failed"`인 항목은 비어있지 않은 실패 사유 필드 `lastError`를 가진다. 이 단언은 **모든 `failed` 아카이브**(finalize-직접실패 경로 포함)를 순회하므로 근거 범위도 순회 범위와 일치해야 한다: **recording §핵심 로직 4(finalize 실패 시 `failed`+사유 기록) + P-2(재시작·세그먼트 소실 복구실패 경로에서 `lastError` non-empty) + 아래 델타의 "모든 `failed` 종단 전이 → non-empty `lastError`" 확장 단언**. (P-2만으로는 finalize-직접실패 경로가 근거에서 빠지므로 세 근거를 함께 인용한다.) (`failed` 항목 존재 전제 — 위 vacuity 가드.)
- **A5 (핵심)** — 미완료 아카이브(`protecting`/`pending`/`finalizing`/`processing`)에 대한 `GET /api/archives/{id}/download`는 **2xx + 미디어 본문을 반환하지 않는다**(비-2xx, 예: 409로 거절). 즉 200 상태로 부분·손상 파일이 내려오지 않는다.
- **A6 (핵심)** — `status == "completed"`인 아카이브에 대한 다운로드는 2xx, `Content-Type: video/mp4`, 비어있지 않은 본문을 반환한다.
- **A7** — 동일 아카이브를 반복 조회할 때 한 번 `completed`가 된 항목은 이후 조회에서 미완료로 되돌아가지 않는다(종단 단조성). 단조성 판정은 상태 값의 불변만 관측하므로 **`completedAt` 델타를 요구하지 않는다** — 따라서 델타 착지 이전에 생성된 **레거시 `completed` 아카이브로도 판정 가능**하다(A3과 달리 SKIP 불요). 다만 `completed` 대상 항목을 확보하려면 아카이브를 실제 `completed`까지 구동한 **mutating fixture**(±스테이징 recorder)가 있어야 하며, 그런 항목이 하나도 없으면 판정 불가로 처리한다.
- **A8** — `failed` 아카이브의 다운로드는 미디어를 반환하지 않는다(비-2xx, 409). 판정 근거는 recording.md 다운로드 계약(아래 델타로 `completed`만 서빙·그 외 비-completed 409·부재 404로 확장)이다.

---

## 단위 B — 다운로드 UX (소비자 측)

### 입력

- 사용자의 보관 요청 액션.
- 아카이브 목록(단위 A가 제공하는 `status` / `completedAt`(UTC RFC3339) / 실패 사유 `lastError`를 담은 표현)의 폴링/재조회 결과.

### 출력 (계약)

- 아카이브 목록 UI의 각 행(row)은 자신의 `status`에 따라 다음을 표면화한다.
  - **미완료 행**: 활성화된 다운로드 컨트롤을 제공하지 않는다(비활성 또는 부재). 요청 피드백은 "요청됨/처리 중"류 진행 상태를 보인다.
  - **`completed` 행**: 활성화된 다운로드 액션을 제공하고, 요청 피드백은 "준비됨"(ready/완료) 상태로 전이하며, **준비 시각(`completedAt`)을 로컬 시각으로 변환해 표시**한다(고아 필드 방지 — 값의 정본은 UTC).
  - **`failed` 행**: 실패 표시(사유 = recording.md의 `lastError` 문자열 포함)를 보이고 다운로드를 제공하지 않는다.
- 보관 요청 직후, 아카이브 목록이 **가시화(자동 펼침)**되어 사용자가 별도 탐색 없이 상태 변화를 관측한다.
- 알 수 없는/미지의 `status` 값이 오면 **안전 fallback**으로 **미완료로 취급**한다(다운로드 미제공, 임의의 "준비됨/완료" 표시 금지).

### 핵심 로직 (동작)

- 다운로드 affordance는 오직 `completed`에만 게이트된다 — 미완료/`failed`/미지 상태에서는 활성 다운로드가 존재하지 않는다.
- 요청 피드백 문구/상태는 상태 전이를 따라 "요청됨/처리 중" → (완료 시) "준비됨"으로 이동한다. `failed` 시에는 실패 상태로 이동한다.
- **폴링 중 서버 오류(5xx)**: 폴링 응답이 5xx(recording 재시작 중 web-backend 프록시 502 포함)이면 이를 아카이브 **실패로 표기하지 않는다** — "처리 중" 상태를 유지하고 다음 폴링 주기에 재시도한다. 실패 표면화는 아카이브 자체 `status == "failed"`에만 반응한다(전송 계층 오류 ≠ 아카이브 실패).
- UI는 아카이브가 종단 상태(`completed`/`failed`)에 도달한 뒤 **유한한 폴링 윈도우 내에** 그 종단 상태를 반영한다(무한정 "처리 중"에 머무르지 않는다). 폴링 상한(web-frontend 5분)에 도달해도 종단 상태가 관측되지 않으면 UI를 "처리 중"에 **고착시키지 않고** 중립 상태("확인 필요 / 새로고침 유도")로 전이한다 — 즉 상한 초과는 영구 "처리 중"이 아니라 사용자에게 재조회를 유도하는 상태다.

### 검증 단언 (TDD)

- **B1 (핵심)** — 상태가 `completed`가 아닌 아카이브 행은 활성화된 다운로드 컨트롤을 노출하지 않는다(다운로드 버튼이 비활성이거나 없음).
- **B2 (핵심)** — 상태가 `completed`인 아카이브 행은 활성화된 다운로드 액션을 노출하고, 그 액션이 미디어를 획득한다(관측: 클릭 시 다운로드가 개시됨/링크가 활성).
- **B3 (핵심)** — 아카이브 상태가 `completed`로 전이하면 사용자 대면 요청 피드백이 "준비됨/완료" 상태(관측 가능한 UI 텍스트/상태)로 전이한다. 그 전에는 "요청됨/처리 중"류 진행 상태를 보인다.
- **B4 (핵심)** — 상태가 `failed`인 아카이브 행은 실패 표시(사유 = recording.md의 `lastError` 문자열 포함)를 노출하고 다운로드를 제공하지 않는다. (재시도 한계는 접합부 참조 — 동일 키 재요청은 기존 `failed`를 반환하므로 UI에서 `failed` 행의 재보관은 자동 재시도가 아니라 DELETE 후 재생성 흐름으로만 성립한다.)
- **B5 (핵심)** — 보관 요청을 하면 아카이브 목록이 가시화(자동 펼침)되어, 후속 상태 변화가 추가 탐색 없이 화면에 관측된다.
- **B6** — 미지의 `status` 값을 담은 아카이브 행은 미완료로 취급되어 다운로드를 제공하지 않으며 "준비됨/완료"로 표시되지 않는다.
- **B7 (timing/bounded)** — 아카이브가 종단 상태에 도달하면 UI는 폴링 윈도우(web-frontend 폴링 상한 = **5분**) **내에** 해당 종단 상태(`completed`의 다운로드 활성 또는 `failed`의 실패 표시)를 반영한다. 5분 윈도우를 **초과**해도 종단 상태가 관측되지 않으면 UI는 "처리 중"에 고착되지 않고 **중립/새로고침 유도 상태**로 전이한다(즉 "처리 중이 아닌 상태로 벗어났는가"를 판정 — 영구 "처리 중"이면 NOK). 폴링 중 5xx/502는 실패 표기가 아니라 "처리 중 유지 + 재시도"로 취급된다.
- **B8 (핵심, 소비)** — `status == "completed"`인 아카이브 행은 준비 시각을 표시한다: `completedAt`(UTC RFC3339)을 로컬 시각으로 변환한 텍스트가 행에 관측된다(`completedAt` 고아 필드 방지). needs-browser(Playwright)면 SKIP.

---

## (횡단) 접합부 — 아카이브 표현 계약

단위 A가 산출하고 단위 B가 소비하는 **아카이브 항목 표현**이 두 단위 사이의 이음새다. 이 이음새는 어느 단위 내부에도 숨지 않고, 다음 접면 SSOT가 소유·규정한다.

- **status enum 값 정의**: recording 서비스 스펙(`docs/spec/recording.md`) — 6종 `{protecting, pending, finalizing, processing, completed, failed}`.
- **소비자 의무(6종 전부 처리 + 미지 상태 안전 fallback + `failed` 명시 노출)**: `docs/spec/interface-web-api.md` §계약 8.

**한계(scope 밖) — `failed` 재보관 경로**: `archiveId = {incidentId}_{streamKey}_{fromUTC}`는 dedup 키이므로(recording.md §출력), 동일 (incident, streamKey, from) 재요청은 **기존 항목을 반환**한다. 따라서 한 아카이브가 `failed`로 고착되면 **동일 키 재요청은 그 `failed` 레코드를 그대로 반환**한다 — `failed` 항목의 재보관은 **`DELETE /api/archives/{id}` 후 재생성으로만** 성립한다. `failed` → 자동 재시도(동일 키 재실행) 경로는 **본 스펙 범위 밖**이며, 이 계약은 그 한계를 명문화하는 데 그친다.

### API 계약 델타 (선언적 — 본 스펙에서 SSOT 파일을 직접 수정하지 않음)

아래는 위 접면 SSOT가 반영해야 할 계약의 델타를 **선언적으로 명시**한 것이다. 실제 SSOT 문서 편집은 오케스트레이터/해당 접면 소유 세션의 몫이다.

> **델타 라우팅 원칙**: `docs/spec/interface-web-api.md` §계약 8은 **인증 게이트 + 투명 프록시**(바디 변형·필터링 없음, §계약 8 핵심 로직 참조)라서 **새 응답 필드를 생산하지 못한다**. 따라서 아카이브 항목의 신규 필드(`completedAt` 등)는 **생산자인 recording 서비스(`docs/spec/recording.md`)의 델타**로 라우팅되며, §계약 8은 그 필드를 그대로 통과시킬 뿐이다.

- **[recording.md로 라우팅] `completedAt` 필드**: 아카이브 항목 표현에 **`completedAt`**(RFC3339, UTC; `status == "completed"`일 때 non-null; 그 외 null/부재) 필드가 존재한다. 이 델타는 **`recording.md` §출력(아카이브 메타데이터 항목)·§핵심 로직(finalize/복구의 `completed` 전이)에 반영**되어야 한다 — 즉 아카이브가 `completed`로 전이할 때 `status`·`sizeBytes`와 **원자적으로 `completedAt`을 기록**(셋 중 하나만 참인 중간 상태가 소비자에게 관측되지 않음, 단위 A 핵심 로직과 정합)한다. 오케스트레이터는 이 델타를 **recording SSOT(recording.md) + recording 코드**에 착지시킨다. (A3은 이 델타 착지 이후에만 판정 가능 — 미착지 시 SKIP, 착지 이후 생성 아카이브에 한정.)
- **[recording.md의 기존 필드로 통일] 실패 사유 필드**: 실패 사유 필드명은 recording의 실제 필드 **`lastError`**로 통일한다(신규 필드가 아니라 recording.md에 이미 존재, P-2에서 `status == "failed"`일 때 non-empty 보장). 본 스펙·§계약 8의 "error/reason" 등 잠정 명칭은 모두 `lastError`를 가리킨다. **[오케스트레이터 머지몫] 근거범위 확장**: 현재 recording.md는 `lastError` non-empty를 **P-2(재시작+세그먼트 소실 복구실패 경로)** 에서만 단언하나, A4는 finalize-직접실패 경로를 포함한 **모든 `failed` 종단 전이**를 순회한다. 따라서 오케스트레이터는 recording.md에 **"모든 `failed` 종단 전이 → non-empty `lastError`" 단언을 추가**(P-2를 finalize-실패 경로까지 확장)하여 A4의 순회 범위와 근거 범위를 일치시킨다.
- **[recording.md §HTTP API `GET /api/archives/{id}/download`로 라우팅] 다운로드 게이트 확장**: recording.md의 다운로드 계약을 **"`completed`만 `video/mp4`로 서빙 · 그 외 모든 비-`completed`(미완료 4종 **및** `failed`)는 비-2xx(**409**) · 아카이브 부재는 404"**로 확장·명시한다(현재 recording.md는 "미완료면 409, 없으면 404"로 `failed` 처리가 명시되지 않음 → `failed`도 409로 명문화). 이 확장이 단위 A의 A8이 참조하는 SSOT 근거다. §계약 8은 이 응답을 투명 프록시하며 통신 실패 시에만 502를 낸다.

## 검증 스킵 선언 (선택)

- **A2·A7 (mutating fixture 의존 명시)** — A2(갓 생성 항목이 not-completed로 관측)·A7(종단 단조성)도 A3/A5/A6/A8과 동일하게 **mutating fixture**(A2=아카이브 생성 POST 구동, A7=아카이브를 `completed`까지 구동; ±스테이징 recorder)에 의존한다. · **A2**: vacuity 가드도 SKIP 선언도 없으므로 명기 — 생성 POST(mutating 게이트, `ALLOW_MUTATING`/`SPEC_TDD_ALLOW_MUTATING`) 없이는 갓 생성한 대상 항목을 확보할 수 없어 **판정 불가/SKIP**. · **A7**: 단조성은 `completedAt` 델타 불요라 **레거시 `completed` 아카이브로 판정 가능**(SKIP 불요)하나, `completed` 대상 항목 자체는 mutating fixture로 확보해야 하며 그런 항목이 없으면 판정 불가. · 중요도: **핵심**(A2·A7). · 해소 조건: mutating 게이트 승인(INDEX §mutating 승인 1) + (A7 대상 확보 시) 스테이징 recorder.
- **A3·A5·A6·A8** — 사유: 이 단언들은 아카이브를 실제로 `completed`/`failed`까지 구동해야(A3=`completed`행 + 델타 착지, A5=미완료행 다운로드, A6=`completed` 다운로드, A8=`failed` 다운로드) 판정 가능한데, 이는 **스테이징 recorder(더미 RTMP + 격리 아카이브 볼륨) + mutating 게이트**(`ALLOW_MUTATING`/`SPEC_TDD_ALLOW_MUTATING`, INDEX §mutating 승인 1)에 의존한다 — B7과 동일한 인프라 의존. · 중요도: **핵심(load-bearing)**(A3·A5·A6), **부수**(A8). · 해소 조건: **INDEX §SKIP조건 5**(recording 스테이징 recorder = 더미 RTMP + 격리 볼륨) 확보 시 즉시 판정. (A3은 추가로 `completedAt` 델타의 recording 착지가 선행 조건.)
- **B7** — 사유: 실시간 폴링 윈도우 내 종단 상태 반영은 브라우저 세션(Playwright) + 라이브 스택(아카이브를 실제로 `completed`/`failed`까지 진행시키는 스테이징 recorder)이 함께 있어야 관측 가능. · 중요도: **핵심(load-bearing)** · 해소 조건: Playwright 세션 + 스테이징 recorder(더미 RTMP + 격리 아카이브 볼륨, INDEX §SKIP조건 5)로 아카이브를 종단까지 구동할 수 있을 때 즉시 판정.
- **B8** — 사유: 완료 행의 준비 시각(`completedAt`) 표시는 needs-browser(Playwright)로 DOM 텍스트를 관측해야 판정 가능하고, `completedAt` 델타의 recording 착지에도 의존. · 중요도: **핵심(소비 단언)** · 해소 조건: Playwright 세션 + `completedAt` 델타 착지 시 판정.
