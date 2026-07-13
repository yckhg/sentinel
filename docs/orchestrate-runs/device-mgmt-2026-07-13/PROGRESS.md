# 진행 (재개용) — 장치 관리 2잎

작업 단위(독립, 병렬 가능):
- [TODO] sensor-device-lifecycle
- [TODO] camera-change-propagation

메모:
- 두 잎 모두 services/web-backend/를 건드림 → 머지 순서 직렬화 또는 봉투 스코프 분리로 라우팅(main.go)·interface-web-api.md 충돌 회피.
- 각 잎은 STEP 0.5 spec-cross-validate 재검증(개정 후 최종본 미재검증) 후 구현 진입.

로그:
- [STEP0-OK] 두 잎 모두 4-항목 분해게이트 통과 · 41cfb39
- [SPEC-XVAL R1] 6렌즈(risk·assumption·alternatives ×2잎) + 코드recon 완료. 두 잎 모두 CRITICAL 검출:
  · sensor: (C) 기존 SSOT 자동복원 계약(interface-web-api 계약6 L340/347, web-backend 단언K, :661)과 신 sticky 단언 E/F 상호배타·델타 미열거 / (C) incidents.go:170 device upsert가 deleted_at=NULL로 삭제센서 무음복원(H2·E·F 동시붕괴) + seen도 deleted_at=NULL / (C) 재출현 "1회" dedup 상태필드 부재·강제 단일 ON CONFLICT로 first판별 불가 / (H) last_seen NOT NULL DEFAULT now→오프라인대기 null 불가 / (H) internal 경계 I 메커니즘 부재·무인증 정책과 충돌 / (H) 재출현 경보 유실 backfill 부재 / (H) /restore 잔존→단일경로 위반 / (H) POST /api/devices·admin게이트 미구현(코드) / (M) alert_state·alias *string·권한통일·재활성 last_seen.
  · camera: (C) incidents에 stream_key 컬럼 없음→단언 B(a) 항진/스키마변경 강요, 실 stream_key 증거는 recording ArchiveMetadata / (H) 단언 A 비동기 관측프로토콜 부재 / (H) 단언 A는 env-override httptest로 상시화 가능→load-bearing SKIP 해소 / (H) 보호아카이브 판정기준 미규정·in-memory 휘발 / (H) 단언 B 항진→삭제 stream_key 아카이브 능동조회로 강화 / (M) "tx 커밋"→autocommit 표현.
  · 코드 사실: recording reload수신·reconcile·기동재동기 이미 완비(갭=web-backend triggerRecordingReload). devices 스키마 완비(surrogate PK·UNIQUE·deleted_at·alert_state). main.go 충돌 낮음(camera=cameras.go만, sensor=main.go 1줄+devices.go).
  · 판정: 두 잎 모두 스펙 정정 필요 → R1 정정 후 재-xval.
- [SPEC-XVAL R1-fixed] sensor=55382ee camera=1e2bb2c 커밋. R2 재검증 디스패치.
- [SPEC-XVAL R2] 6렌즈 재검증 완료. R1 정정은 방향 견고, 잔여 HIGH 검출:
  · sensor HIGH: (H-1) reappear dedup 동일-초 오탐 → rowcount 가드 UPDATE(NULL→non-NULL 전이) / (H-2) incidents 경로 재출현 브로드캐스트 부재 — 공유 dedup 컬럼이 위기-우선 유입 시 device_reappeared 영구 미발행(안전누수) / (H-3) null last_seen health 정합 TDD 게이트 부재(health_summary 2쿼리+health.go evaluateSensors Scan) / (H-4) 단언 I 과대주장(/api/incidents 무인증 자동등록 잔존) → I를 seen 한정. MEDIUM: INTERNAL_TOKEN fail-closed+compose 2서비스 델타, SQLite 테이블 재구축, backfill re-nag, 델타 파일목록(equipment.go 제외), H2 write 패턴정밀, reappear_alerted_at 근거정정, C2 라벨 강등.
  · camera HIGH: (H-1) B2(b) FilePath=병합 MP4(‥.ts 세그먼트 아님) → 판정 대상 MP4+metadata.json으로 정정. MEDIUM: reconcile 완료신호(GET /api/status 폴링), 유효 미디어 픽스처+completed&&sizeBytes>0, 픽스처 타임스탬프 창, archives 클라이언트필터, 라이브recorder 분리, 프롬프트위생, B1→B1s/B2 흡수, end-to-end→선택 스모크, closed-port 계측기.
  · 코드 affirm: env-override URL·protect/finalize API·Reload 아카이브 무접촉·in-process admin주입 패턴·sticky 타깃·O4 스냅샷·재활성 backfill 경계 모두 실재 확인.
  · 판정: 두 잎 R2 정정 후 R3 재-xval.
- [SPEC-XVAL R2-fixed] sensor=d37b546 camera=21a3a9b 커밋. R3 재검증 디스패치.
- [SPEC-XVAL R3] 6렌즈. camera=전 렌즈 HIGH0(코드검증 완료) → SPEC-OK. sensor=5렌즈 clear, risk 1 HIGH(hw-gateway 헤더 동봉 무검증·fail-closed 폭발반경) → R3 정정(단언 L·I2·H2 스코프) 커밋 d110f2a → sensor-only R4 재검증.
- [SPEC-OK] camera-change-propagation · R3 · 21a3a9b
- [SPEC-OK] sensor-device-lifecycle · R4 · 6860a72
- [SPEC-XVAL R4] sensor 3렌즈(risk·assumption·alternatives) 전원 HIGH0 — 확정 가능. R3 추가분(L·I2·H2) 코드검증 완료. R4 렌즈 권고(L 콜사이트 결속·H2 표기정규화) 반영 커밋.
- [IMPL camera] 구현 완료 d06a52e (feat/camera-change-propagation): triggerRecordingReload 3핸들러 배선. A green·B1s green·B2 SKIP(load-bearing). go test -race 통과. → STEP D 독립검증 디스패치.
- [VERIFY camera] R1·R2 독립검증(비공허성 능동입증) → R3 정적순서 가드 하드닝. CRITICAL/HIGH 0 전 라운드. 잔여=B2 load-bearing SKIP(사람승인)+수용 nice-to-have. camera HEAD 8cbd3a0. → STEP E 최종 독립 게이트.
- [IMPL sensor] 구현 완료 2bebd17 (feat/sensor-device-lifecycle): migration 21(테이블재구축 nullable)+22(reappear_alerted_at), POST /api/devices, sticky(deleted_at=NULL 제거 양경로), maybeAlertReappear rowcount 공유헬퍼, internal-token fail-closed, /restore 제거, null health offline, WS broadcast+backfill, 프론트. 상시 9 PASS(A·B·C·C2·H2·I·I2·J+migration), mutating 9 SKIP(A2·D·E·E2·F·F2·G·H1·K), L OK. build/vet/test-race clean. → STEP D 독립검증.
  · 오케스트레이터 노트: (1) 프론트 device_reappeared를 app-root 단일소켓 스코프 밖이라 /api/devices/all 폴링으로 표면화(백엔드 WS broadcast+backfill은 완비) — K needs-browser SKIP·사람승인 대상. (2) notification-test-send 잎 기존 flaky(이 변경 무관). (3) INTERNAL_TOKEN compose 주입은 docker-compose.yml 스코프 밖 → 오케스트레이터 머지 시 양 서비스 주입 필수(fail-closed).
- [DONE] camera-change-propagation · tests=A(in-process 3소비자,8서브)·B1s(정적)·정적순서게이트 passed · skipped=B2(load-bearing,mutating,격리스택,사람승인) · merge f1a88cb (impl 8cbd3a0)
  · SSOT 델타(interface-web-api·web-backend·recording) 반영은 sensor 머지 후 일괄(공유 문서 이중편집 회피).
- [VERIFY sensor R1] 상시 9단언+migration OK·비공허 능동입증, mutating 8+K 진짜 SKIP. CRITICAL/HIGH 0. finding: MEDIUM-1(프론트 WS 폴링 이탈=사람승인 이탈, 불변경) MEDIUM-2(핫경로 이중write) LOW-4(동시 409) LOW-5(tx) nice-6(alert_state) → R2 라우팅(MEDIUM-1 제외).
- [VERIFY sensor R2] 상시 단언 OK·비공허(H2 위임스코프 RED), LOW-5/4 정확, 회귀 없음. CRITICAL/HIGH 0. finding: F1(once-only 상시테스트 공허·초해상도 의존) F2(핫경로 zero-write 미게이트) F3(tx 오버헤드 스펙상 불필요) F4(alert_state 비대칭) → R3 라우팅. 두 load-bearing 주장(정확히1회·zero-write)을 상시 게이트化.
- [IMPL sensor R3] e1ff101: F1(once-only 상시게이트 강화·초해상도 비의존·broadcast 카운트) F2(zero-write 게이트·guard 호출 seam) 둘 다 RED 실험 확인. F3(핫경로 tx 제거·직렬 단일문 RETURNING 가드) F4(alert_state 대칭). 11 PASS+9 SKIP. → STEP E 최종 게이트.
- [DONE] sensor-device-lifecycle · tests=A·B·C·C2·H2·I·I2·J·L·migration·once-only강화·zero-write passed · skipped=A2·D·E·E2·F·F2·G·H1(mutating,load-bearing)·K(needs-browser) · merge eb91d0f (impl 06e121f)
- [INTEGRATION] 교차단위 충돌(funcBody redeclared) 해소 개명 커밋. 격리 컨테이너 통합 build/vet/test -race green. → SSOT 델타·compose·RESULT.
- [SSOT-DELTA] 접면 SSOT 4문서 정합: recording(206570b, camera 3소비자·증거보존) + interface-web-api·web-backend·hw-gateway(8eb03d3, sensor sticky·재출현·X-Internal-Token). 구현자 편집 → **독립 검증자** 11항목 전원 OK·신구 상호배타 잔재 0(코드 정본 대조). 문서만 변경(코드 무변경 → 통합 빌드 green 유효). compose INTERNAL_TOKEN 배선은 1fb7a54 기반영.
- [RESULT] 모든 작업 단위 DONE·SSOT 정합 완료 → RESULT.md 작성. 통합 브랜치 HEAD 8eb03d3. main 머지·push는 사람 결정.
