# Spec 준수 감사 리포트 (2026-07-10)

> 대상: `docs/spec/` 스펙 11종 — 서비스 8 (hw-gateway, cctv-adapter, youtube-adapter, streaming, recording, notifier, web-backend, web-frontend) + 인터페이스 3 (interface-mqtt, interface-streaming, interface-web-api).
> 방법: **스펙 1건당 독립 검증자 1명(총 11명)** 이 배정 스펙의 검증 단언을 실제 running/healthy 스택에서 판정.
> 원칙: **비파괴만 실행**(`ALLOW_MUTATING`/`SPEC_TDD_ALLOW_MUTATING` 미설정) · **코드-실측 타이브레이크**(스펙↔코드 충돌 시 현재 코드를 기본 진리로, 스펙 이상형 어긋남은 NOK가 아니라 "spec 결함 후보"로 분리) · **강제-5 래칫 1라운드**(자동 검사 전건 초록이어도 검증자당 오류·개선 finding ≥5 채굴).
> **작성자·검증자 분리**: 이 리포트는 11개 독립 검증 리포트를 종합한 편집 산출물이며, 판정 자체는 각 검증자가 수행함. 편집자는 코드·테스트·스펙 계약 문서를 수정하지 않음.
> 상세 근거: 각 스펙의 단언별 판정표·재현 커맨드·finding 전문은 스펙별 검증 리포트 참조 (본 문서 각 절 각주).

---

## 헤드라인 결론 — 감사는 "스테일 컨테이너"를 측정했다 (미배포 드리프트)

**이 감사의 런타임 판정 상당수는 오늘자(2026-07-10) 수정이 반영되지 않은 4월 구버전 컨테이너를 측정한 것이다. 코드 결함이 아니라 배포 드리프트다.**

하드 확증 사실:

- **실행 컨테이너 이미지 생성 시각**: web-backend=**2026-04-27**, notifier=**2026-04-22** — 약 **2.5개월 전** 빌드.
- **오늘자 fix 커밋 전부 미배포** — 저장소에만 존재: `c6f0ba1`(`/internal/alarms` 신설, #19), `7af33db`(incident 멱등 alertId forward + 5xx 재시도, #20/#22), `a02a700`(web-frontend 타임라인 마커 + 생존판정 config 소비, #21/#23), `5b83141`(spec-tdd NOK 6건 해소), `f988d73`/`4e99ce8`(#24 MQTT retained 수신측 방어). 즉 07-02 리포트 §A의 실결함 수정 A-1~A-5 및 #19~#25가 **전부 저장소에만 있고 실행 스택에는 없다**.
- **하드 프로브**: 실행 중 web-backend가 `POST /internal/alarms`에 **404** 반환. 그러나 저장소 코드에는 라우트가 존재(`services/web-backend/main.go:94`, `alarms.go:10`). 배포본과 소스가 갈린 직접 증거.

**해석 — 검증자의 "코드-실측"이 두 세계를 섞었다.** 검증자들의 정적 코드 판정은 **수정된 저장소**를 본 반면, 런타임 프로브(로그 관측·직접 호출)는 **4월 구버전(수정 이전) 컨테이너**를 측정했다. 둘이 갈린 지점 — notifier system-alarm 79/79 **401**(구 notifier가 pre-fix 엔드포인트 `/api/alarms`를 호출 → auth 미들웨어 401), `/internal/alarms` 직접 프로브 **404**(구 web-backend에 라우트 부재), incident 멱등(alertId forward·5xx 재시도), web-frontend 타임라인 마커·생존판정 config — 은 **전부 코드 결함이 아니라 미배포 드리프트**다. 따라서 **이 감사의 런타임 판정 상당수는 스테일 컨테이너 재배포로 오늘자 fix를 활성화한 뒤 재검증해야 유효**하다. (정적 계약 준수 판정과 §5a 보안 클러스터는 소스 기준이므로 별개로 유효.)

---

## 1. 집계

| Spec | OK | NOK | SKIPPED | 총 | 실행률(실행/전체) |
|---|---:|---:|---:|---:|---:|
| interface-mqtt | 0 | 0 | 22 [^1] | 22 | 0% |
| interface-streaming | 9 | 0 | 8 | 17 | 53% |
| interface-web-api | 11 | 0 | 73 | 84 | 13% |
| hw-gateway | 1 | 0 | 16 | 17 | 6% |
| cctv-adapter | 2 | 0 | 9 | 11 | 18% |
| youtube-adapter | 4 | 0 | 5 | 9 | 44% |
| streaming | 3 | 0 | 5 | 8 | 38% |
| recording | 13 | 0 | 2 | 15 | 87% |
| notifier | 8 | 0 | 2 | 10 | 80% |
| web-backend | 3 | 0 | 16 | 19 | 16% |
| web-frontend | 2 | 0 | 12 | 14 | 14% |
| **합계** | **56** | **0** | **170** | **226** | **25%** |

[^1]: interface-mqtt는 검증 단언 **22개** vs 테스트 파일 **21개** — RS-6(retained resolve 드롭, 안전 단언)에 대응하는 테스트 파일이 부재(F-2 참조). 07-02 INDEX는 이 스펙을 "SKIP 21"로 표기했으나 단언 기준 22가 정확. 검증자 리포트(interface-mqtt)를 신뢰해 22로 계상.

---

## 2. 핵심 결론 — "초록의 대부분은 SKIPPED(공허)"

**NOK는 0건이다. 그러나 이 초록을 준수로 읽으면 안 된다.**

- 전체 226 단언 중 **실측 실행된 것은 56건(약 25%)뿐**이고, **170건(약 75%)이 SKIPPED**다. SKIPPED는 초록(OK)으로 세지 않는다.
- SKIPPED 사유는 세 갈래다: (a) **mutating-gate** — 발행/incident 생성/알림 발송/컨테이너 재생성 등 프로덕션을 변조하므로 비파괴 원칙상 미실행, (b) **fixture 부재** — admin/user JWT(`ADMIN_PASSWORD`/`ADMIN_TOKEN`/`USER_TOKEN`) 미주입으로 인증 계약 판정 불가, (c) **no-data / needs-browser / no-test** — 표본 0(카메라 0대·protecting 아카이브 0·0바이트 파일 0), 브라우저 세션 필요(web-frontend), 테스트 파일 자체 부재(RS-6).
- 실행된 56건도 상당수가 **정적 계약(스키마·라우트·상수·엔벌로프)**·**부정 경로(401/400/404)**·**무인증 internal 라우트**에 국한된다. 즉 **정적 계약 준수는 확인됐으나, load-bearing 런타임 계약**(멱등성, 브로커 단절 내성, alive→dead SSOT 전이, 임시링크 폐기, 최후 보루 전파, finalize 병합, 위기 배너 상호작용 등)**의 실측 검증은 대부분 미수행**이다.

둘째 축(위 헤드라인): 실행된 56건 중 **런타임 프로브 계열은 4월 구버전 컨테이너를 측정**했다. 즉 이번 감사는 두 겹으로 공허하다 — (1) 75%가 SKIPPED, (2) 실행분의 런타임 판정마저 스테일 이미지 기준. **재배포 후 재검증이 전제**되어야 이 리포트의 런타임 판정이 오늘자 시스템을 대표한다.

이 리포트의 실질 가치는 "NOK 0"이 아니라 **§4 검증 스킵 목록(무엇이 아직 검증되지 않았는가)** 과 **§5 finding(정적 분석·과거 로그·실측으로 드러난 실결함/위험)** 에 있다.

---

## 3. 스펙별 한 줄 요지

| Spec | 요지 |
|---|---|
| interface-mqtt | 전 단언(22)이 mutating 게이트 또는 그 산출물 의존 → 비파괴로 **단 한 건도 초록 불가**. RS-6 안전 단언은 테스트 파일조차 없음. |
| interface-streaming | HLS 출력 규격·상대 URL·RTMP pull 등 정적/read-only 계약 9건 OK. 상태 전이(active→inactive)·B-frame 거부 등 핵심 6건 SKIPPED. healthz 게이트 false-NOK 버그. |
| interface-web-api | 84 중 11만 실행(무인증 internal + 401/400 부정 경로). admin/user JWT·mutating 계약 73건 판정 불가. |
| hw-gateway | healthz 1건만 실행. 핵심 런타임 계약 11건(dedup 멱등성·브로커 단절·timestamp 위생 등) mutating-gated 미실측. |
| cctv-adapter | **카메라 0대 구성** → push/스트리밍 핵심 8건 실측 불가(no-data + mutating). healthz·빈 상태만 OK. |
| youtube-adapter | 실행 4건(healthz·상태·localFile 루프·RTMP 규격) OK. URL 검증·reload·정상종료 등 5건 mutating-gated. |
| streaming | 정적 3건(healthz·상대 URL·HLS 헤더) OK. SSOT 핵심(dead 전이)이 어떤 비파괴 경로로도 미검증. |
| recording | 자연 데이터로 13건 OK(실행률 87%). 다만 운영 데이터에 다수 이상 징후(§5c). E(0바이트 정리)·L(reload)만 완전 SKIP. |
| notifier | 79건 과거 이벤트 로그 + 코드-실측으로 8건 OK. **최후 보루가 79/79 전량 401(§5b) = CRITICAL.** |
| web-backend | 3건만 OK. admin fixture 부재로 인증 GET 대량 SKIP. **보안 finding 4건(CRITICAL 2 + HIGH 2)** 실측 다수 확인. |
| web-frontend | 정적 2건(뷰포트·서빙) OK. 렌더/상호작용 12건 needs-browser SKIP → Playwright 세션 필요. |

---

## 4. 검증 스킵 목록 (인간 검토면)

> load-bearing(핵심) SKIPPED 단언만 스펙별로 취합. **초록 아님 — 아래는 이번 감사에서 실증되지 않은 계약 목록이다.** 종류: 의도적(mutating-gate 등 설계상 격리) / 부적절(no-data·no-fixture·no-test 등 검증 하니스 미비). 해소조건 = 실판정에 필요한 최소 투입.
> 핵심 SKIPPED 합계 ≈ **136건**(스펙별 핵심 표시 단언의 합; interface-web-api의 mutating 50 + admin 11이 지배적).

| Spec | 핵심 SKIPPED 단언ID | 수 | 종류 | 사유 | 해소조건 |
|---|---|---:|---|---|---|
| interface-mqtt | A-1·A-2·A-3·A-4·H-1·C-1·R-1·R-2·RS-1·RS-2·RS-3·RS-5·RS-6 | 13 | 의도적+부적절 | 전건 mutating(실 incident/해소/재시작); RS-5 물리 디바이스 관측 필요; RS-6 **테스트 파일 부재** | 격리 스택 + `ALLOW_MUTATING`; RS-5 입회 수동; **RS-6 테스트 신규 작성** |
| interface-streaming | A1-1·A1-2·A1-3·A2-5·A4-2·A4-3 | 6 | 의도적 | mutating(RTMP push 발행·B-frame push·상태전이) | 격리 push + `SPEC_TDD_ALLOW_MUTATING` |
| interface-web-api | mutating-gated 50 + admin-token 부재 11 | 61 | 의도적(50)+부적절(11) | `ALLOW_MUTATING` 미설정 + admin/user JWT 미주입 | 격리 DB mutating 레인 + `ADMIN_PASSWORD` 주입 |
| hw-gateway | B·E·F·H·I·J·K·L·N·O·O2 | 11 | 의도적 | mutating(MQTT 발행·incident·알림·브로커 stop·재시작). O/O2는 매우 침습적 | 격리 스택 승인 실행. O/O2는 입회/스테이징 |
| cctv-adapter | C·D·E·F·G·H·I·J·K | 9 | 의도적+부적절 | **카메라 0대**(no-data) + mutating(RTSP push·watchdog SIGSTOP·reload) | 테스트 RTSP 소스 도입 + 격리 실행 |
| youtube-adapter | C·F·G·H·I | 5 | 의도적 | mutating(yt-dlp 해석·RTMP push·소스집합 교체·SIGTERM) | 격리 throwaway 환경 승인 실행 |
| streaming | B·D(후반)·G | 3 | 의도적 | mutating(streaming 컨테이너 재생성·push 중단 후 dead 전이) | 격리 재생성/push 승인 |
| recording | E·L + C-전이·H-finalize404·O-protecting복원(오버레이) | 2+3 | 의도적+부적절 | mutating(0바이트 생성·reload=recorder 중지); protecting 아카이브 n=0 | 스테이징 recorder(더미 RTMP + 격리 볼륨) |
| notifier | D·J | 2 | 부적절 | 링크 라우트 선택적 실패·0-연락처 상태를 프로덕션에서 비파괴 생성 불가 | 격리 스택 fixture(연락처 0 / 링크 실패 주입) |
| web-backend | B·C·D·F·G·H·I·J·L·M·O·Q·R·S | 14 | 의도적+부적절 | mutating 게이트 + admin fixture 부재 + 재시작/infra 조작 | `ADMIN_PASSWORD` 주입 + 격리 mutating/재시작 레인 |
| web-frontend | B·C·D·H·I·J·K [^2] | 7 | 의도적 | needs-browser(렌더/상호작용) | Playwright 세션 별도 실행 |

[^2]: web-frontend 검증 리포트 본문 요약은 "핵심 8건"으로 적었으나 단언표 상 핵심 표시는 B·C·D·H·I·J·K 7건. 표는 단언표 기준 7로 계상.

---

## 5. CRITICAL / HIGH finding — 테마별 그룹핑

> 강제-5 래칫으로 채굴된 finding 중 CRITICAL/HIGH만 테마로 묶음. **CRITICAL 3 · HIGH 약 20 (합계 약 23건).** severity는 각 검증자 부여값. 상세는 각주 리포트.

### (a) 보안 클러스터 — "외부 포트 노출 없음 / Docker 네트워크 격리" 전제에 전량 의존 [^wb][^api][^notif][^hwgw]

이 시스템의 다수 인증·인가·발급 계약은 **네트워크 경계(리버스 프록시가 `/internal/*`·`/api/*`를 외부에 노출하지 않고, Docker 내부망에 침해 컨테이너가 없다)** 전제 위에서만 성립한다. 이 전제가 깨지면 아래가 즉시 악용 가능하다.

- **[CRITICAL] web-backend F1 — 폐기(revoke)된 임시링크 JWT가 blacklist를 우회.** `authMiddleware`/`handleWebSocket`가 일반 `parseJWT`를 **먼저** 시도 → temp 토큰이 일반 파서를 통과(role="")하여 blacklist 확인 분기에 **도달하지 않음**. 폐기된 토큰이 24h 만료까지 `/api/*`·`/ws`를 계속 통과(단언 M의 보장이 실제 보호 자원 경로엔 미성립). interface-web-api F5(HIGH)와 동일 결함.
- **[CRITICAL] web-backend F2 — `POST /api/links/temp` 완전 무인증 발급(실측).** Authorization 헤더 없이 201 + 24h 토큰. 라우트가 `/internal/*`이 아닌 `/api/*` 아래라 프록시가 `/api/*`를 노출하면 **누구나 24h 열람 토큰 자가발급**.
- **[HIGH] web-backend F3 — temp/일반 토큰 과다권한(실측).** 무인증 발급 temp 토큰으로 `/api/incidents·contacts(개인정보 phone/email)·devices·health·cameras` 모두 200. role 미검사 핸들러로 **장비 재시작·장비 CUD·녹화/아카이브 proxy POST/DELETE**까지 실행 가능. "CCTV 열람 전용" 의도 초과.
- **[HIGH] web-backend F4 — rate limiter가 X-Forwarded-For 무조건 신뢰(fail-open).** 직접 노출 시 XFF 위조로 login(10/분)·register(5/분) 제한 무력화. interface-web-api F4와 연동.
- **[HIGH] interface-web-api F4 — 무인증 internal 엔드포인트 외부 노출(리뷰3, 미해소).** 프록시 차단 부재 시 외부에서 `POST /api/incidents`·`/api/devices/seen`·`resolve-from-sensor`·무인증 `/api/links/temp` 호출 가능 → **incident 위조·유령 해소 주입**(산업안전 게이트 우회).
- **[HIGH] notifier F2 — 위기 이메일 SMTP 헤더 인젝션 + HTML XSS.** `sendCrisisEmail`이 외부 유래(MQTT→hw-gateway) `alert.Type/Description`을 subject·body에 **무-sanitize** 삽입. `\r\n` → 헤더 인젝션, `<script>`/`<img onerror>` → 수신 클라이언트 저장형 XSS. `/api/send-email`은 sanitize를 거치나 위기 경로는 비대칭으로 우회.
- **[HIGH] notifier F3 — `sanitizeHTML`의 `javascript:` 필터 우회.** 따옴표 있는 href/src만 매칭 → 따옴표 없는 `<a href=javascript:...>` 통과, 허용태그 `a` 유지로 생존. `data:`/`vbscript:`/CSS `expression()` 미처리. 정규식 sanitize의 구조적 한계 → 속성 화이트리스트 전환 필요.
- (연계) hw-gateway F5(MEDIUM) — `/api/restart`·`/api/test-alert`·`/api/alert/resolved` **무인증**. 침해 컨테이너가 장비 재시작 명령·위조 해소 발행 가능. notifier finding3(MEDIUM) — `/api/notify` 무방비. 전부 동일 "내부망 신뢰" 전제 의존.

**→ 설계자 판단 필요: 네트워크 격리 전제의 실제 성립 여부 검증(프록시 location 차단 감사) + 위 CRITICAL/HIGH 개별 방어(파서 판별 순서·발급 인증·role 검사·XFF 무시·위기 경로 sanitize) 도입.**

### (b) 배포 드리프트 / 운영 단절 — **감사 전체의 헤드라인** [^notif][^api]

> 위 "헤드라인 결론" 참조. 실행 이미지 web-backend=2026-04-27 · notifier=2026-04-22, 오늘자 fix(#19~#25, A-1~A-5) 전량 미배포. 아래 항목들은 개별 코드 결함이 아니라 **동일한 미배포 드리프트의 발현**이다.

- **[CRITICAL] notifier F1 — 최후 보루(system alarm)가 운영에서 79/79 전량 401 → 손실방지 사슬 end-to-end 단절.** KAKAO/SMS 기본 꺼짐(실측) 상태에서 유일 fallback이 720h 로그 전량 실패 → **admin WS `system_alarm` 브로드캐스트가 한 번도 발생 안 함**. **근본 원인은 드리프트**: A-1 수정(`c6f0ba1`, `/internal/alarms` 무인증 200 라우트 + notifier가 신 엔드포인트 호출)이 저장소에만 존재. 배포된 4월 notifier는 pre-fix 엔드포인트 `/api/alarms`를 호출 → 배포된 4월 web-backend의 auth 미들웨어가 401. 별도 하드 프로브에서 신 라우트 `POST /internal/alarms` 직접 호출은 **404**(구 web-backend에 라우트 부재). 소스는 무인증 200 반환하도록 되어 있음(코드 결함 아님). interface-web-api C도 이 라우트를 "코드-실측 OK"로 판정했으나 이는 **저장소 코드를 본 것**이고 실행 커버리지는 0(F3)이라 드리프트를 못 잡음.
- **[재검증 필요] incident 멱등** — hw-gateway가 alertId를 forward하고 web-backend가 alertId dedup + 5xx 재시도(`7af33db`, A-2/A-4)하는 계약은 저장소에만 존재. 4월 실행본은 alertId 미전송 → DB dedup 경로 미발화. hw-gateway 단언 F·interface-mqtt A-2·interface-web-api B의 "코드-실측 정합"은 소스 기준이며 실행 스택에선 아직 미활성.
- **[재검증 필요] web-frontend 타임라인 마커·센서 생존판정 config**(`a02a700`, A-3/A-5), **MQTT retained 방어**(`f988d73`, RS-6 안전 기능) 등도 저장소에만 반영. 정적 근거로 "구현 존재" 확인했으나 실행 스택 기준 미배포.

**→ (설계자 결정 0) 스테일 컨테이너 전량 재배포로 오늘자 fix 활성화 → 위 런타임 판정 재검증 → 배포 후 회귀 게이트(`/internal/alarms` 무인증 200, alertId dedup 발화, retained 드롭) 추가.**

### (c) 운영 데이터 이상 (recording, 자연 데이터 관측) [^rec]

- **[HIGH] failed 아카이브 48/214(22%).** 사유 분포: `no segments in requested range` 44건(빈 구간 finalize/manual을 선검사 없이 수용 → 쓰레기 메타 누적), `ffmpeg merge: signal: killed` 3건(FFMPEG_TIMEOUT 60초 기본값이 **수 시간짜리 병합에도 적용**되어 kill → **incident 증거영상 영구 상실, 재시도 없음**), `read dir … no such file` 1건.
- **[HIGH] `incidentTime` zero-time 수용 → epoch0 아카이브 15건.** 무효 incidentTime이 epoch 0으로 파싱되어 1969-12-31 기준 **약 56년 구간**을 protect/finalize → 거대 보호구간 + 거대 metadata span 아카이브 양산. 입력 검증 부재.
- **[MEDIUM] 보호집합 무한 성장으로 롤링 정리 무력화.** RECORDINGS 세그먼트 13999개 중 **stale-but-protected 13284개**(미보호 stale 0). 삭제된 아카이브의 세그먼트 보호가 해제 안 되면 in-memory 경로 집합·디스크가 재시작 전까지 단조 증가.
- (연계) metadata `to`가 실제 MP4 범위와 불일치(+30분 post-roll이 병합 즉시 실행으로 미반영) → 소비자가 `to`로 seek 시 오작동.

### (d) 교차 서비스 반복 패턴 [^cctv][^yt][^rec][^hwgw][^wf]

- **SIGTERM graceful shutdown 미준수** — cctv-adapter(시그널 핸들러 없음, `docker stop` 시 자식 ffmpeg가 clean SIGTERM 없이 SIGKILL) · youtube-adapter(`StopAll` 직후 `os.Exit(0)`로 5s 유예 goroutine 실행 전 PID1 종료). 컨테이너 teardown이 회수하므로 단언은 통과할 여지 있으나 **계약상 유예 미적용**.
- **API 타임아웃 누락** — web-frontend FINDING-1(HIGH): `RestartDialog`(재시작 POST)·`EmergencyCallButton`(sites GET)이 `fetchWithTimeout` 아닌 raw `fetch` → 백엔드 무응답 시 **재시작 다이얼로그 무한 대기**(안전 명령 UX 정지). hw-gateway SD-1/F-3: outbound `httpClient`가 스펙 10초 vs 실제 `ResponseHeaderTimeout` 5초 캡 불일치.
- **reload가 web-backend HTTP status를 미검사 → teardown 위험** — cctv-adapter finding1(HIGH)·youtube-adapter SD-1·recording finding2(HIGH): reload가 `resp.StatusCode`를 무시하고 body의 빈/`null` JSON 배열을 "정상 0대 reconcile"로 처리 → **200+`[]` 또는 저하 응답 1회에 전체 push/recorder를 teardown**. 안전 모니터링 상시가동 계약과 정면 충돌. cctv·youtube·recording 3개 서비스 공통.
- **reload 중복 streamKey 미방어** — cctv·youtube: 동일 streamKey 2행 시 count(2) ≠ status 목록 정합성 깨짐, 같은 RTMP key로 goroutine 2개 push.

### (e) 테스트 인프라 버그 (게이트 자체 결함) [^strm][^ifstrm][^ifmqtt][^api]

- **[HIGH] healthz 게이트 false-NOK** — streaming F-1 · interface-streaming finding1: `A_healthz.sh`/`A5-1_healthz.sh`가 `wget -S -qO-` 본문에 **trailing newline이 없어** body와 첫 헤더줄이 한 줄로 병합 → `jq` parse error → **정상 구현을 항상 NOK로 오판**. 깨진 게이트가 진짜 상태를 가림. 수정: 상태코드/body 취득 분리.
- **[HIGH] RS-6 retained-resolve 테스트 파일 부재** — interface-mqtt F-1: 커밋 #24(`f988d73`, "MQTT retained 수신측 방어 — incident 무인 자동해소 차단")의 핵심 안전 기능(코드엔 `isRetainedMessage` 구현됨)에 **회귀 테스트 게이트가 없음**(22 단언 vs 21 테스트). 사람 게이트를 지키는 안전 기능이 무방비.
- **[전 검증자 공통] fixture 부재로 대량 skip** — interface-web-api F6: 84 중 73(87%)이 admin/user 토큰 미주입 + mutating 게이트로 비실행. web-backend도 동일. `ADMIN_PASSWORD` fixture + 격리 DB mutating 레인 없이는 인증·상태변경 계약을 배포 게이트로 전혀 강제 못함.
- (연계) 사후관측 테스트 취약성 — recording finding9: D/F/K/M/O가 자연 데이터 의존, 데이터 없으면(protecting n=0) 서브단언이 조용히 미검증. interface-mqtt F-4: h1/h4/h5가 Go json.Marshal 필드 순서에 하드코딩 결합.

---

## 6. spec 결함 후보 (코드-실측상 문서 정정 대상 — 설계자 판단)

> 코드-실측 타이브레이크로 분리된 항목. **코드가 진리이므로 NOK 아님.** 스펙 본문(또는 서비스 가이드)을 코드에 맞춰 정정할지가 설계자 결정.

| # | Spec | 결함 후보 | 정정 방향 |
|---|---|---|---|
| SD-mqtt-1 | interface-mqtt | **에러정책표 모순**: 본문(`interface-mqtt.md:563`)="5xx 포함 HTTP 응답 받으면 재시도 안 함" vs 코드(`main.go:483-509`)="5xx는 최대 3회 재시도"(함수 주석도 5xx 재시도 명시). web-backend alertId dedup으로 중복 incident 없음 → 코드 안전. | 표를 코드에 맞춰 "5xx 재시도" 로 정정 |
| SD-mqtt-2 | interface-mqtt | incident 출력 필드 목록에 `alertId` 누락(본문 line135) — 실제 전송·dedup 키인데 계약에서 빠짐 | 출력 필드에 alertId 추가 |
| SD-strm-1 | streaming / interface-streaming | **`startedAt`=playlist mtime**(시작 시각 아님). 3초 폴링마다 값 전진 실측 → 필드명·예시가 "시작 시각"을 암시하나 구현은 최종 수정 시각 | 필드명 정정(`lastUpdatedAt`) 또는 실제 시작시각 추적 |
| SD-strm-2 | interface-streaming / youtube | H.264 profile이 push측에서 pin 안 됨(`-preset ultrafast` 부작용으로 우연히 Baseline). preset 변경 시 조용히 바뀜, 검증 단언도 없음 | push측 `-profile:v`/`-bf 0` pin + profile 값 단언 추가 |
| SD-cctv-1 | cctv-adapter | reload가 web-backend 2xx+빈/`null` 배열 거동 미규정 → 현 코드는 전 카메라 teardown | "빈 목록 = no-op 가드" 확정 |
| SD-yt-1/2 | youtube-adapter | reload가 config localFile 스트림 전면 폐기(web-backend에 localFile 개념 없음); localFile 전용 소스가 youtubeUrl 검증에서 탈락 | config↔web-backend SSOT 정의 |
| SD-rec-1 | recording | reload가 web-backend 조회 실패를 "카메라 0대"로 취급(⚠️8); metadata `to`가 실제 MP4와 분리(⚠️2); incidentTime zero-time 수용 | 조회 실패↔0대 구분, `to` 정정, incidentTime 검증 |
| SD-hwgw-1 | hw-gateway | forward 타임아웃 계약(10초) vs 실효(ResponseHeaderTimeout 5초) 불일치; dead 판정 시 alertState 처리 규정 공백(alive:false+alertState:active 노출) | 타임아웃 계약/구현 정합, dead 시 alertState 계약 추가 |
| SD-wb-1~3 | web-backend | R의 "cam-{8hex} 유일 자동부여"가 전칭 서술 vs 시드 `yt-cam-1/2` 위배; 재시작·장비 CUD가 admin 목록에서 누락(§51); 테스트알림 경로 가이드↔코드 불일치 | "생성 응답 한정" 명시 또는 시드 규격화; authz 목록 확정 |
| SD-wf-1~4 | web-frontend | 세션 중 401 미처리(진입 1회만 exp 검사); `incident_resolved` WS 미소비(위기 배너 유령 잔존); 녹화 타임라인 창 mount 고정 | 각 항목 의도(축소/미구현) 확정 |
| SD-notif | notifier | 단언 C의 "web-backend 무인증 2xx 응답" 전제가 실측 401과 모순(§5b 원인); `contactCount:0` 고정 | 배포 드리프트 해소 후 문구 정합 |

---

## 7. 설계자 결정 필요 목록

0. **[최우선] 스테일 컨테이너 재배포로 오늘자 fix 활성화 후 런타임 재검증(헤드라인·§5b).** 실행 이미지(web-backend 2026-04-27, notifier 2026-04-22)가 오늘자 fix(#19~#25, A-1~A-5)를 전혀 반영하지 못한 상태다. **전 서비스 재빌드·재배포가 다른 모든 재검증의 선결 조건**이다. 재배포 없이 얻은 런타임 판정(notifier 401, `/internal/alarms` 404, incident 멱등 미발화, 타임라인 마커, retained 방어)은 오늘자 시스템을 대표하지 못하므로, 재배포 후 해당 단언을 다시 판정해야 한다.
1. **보안 클러스터 처리 방침 + 네트워크 격리 전제 검증(§5a).** 리버스 프록시가 `/internal/*`·`/api/links/temp` 등을 외부에 노출하지 않는지 실제 감사하고, 전제가 불충분하면 개별 방어(temp 토큰 파서 판별 순서·발급 인증·role 검사·XFF 무시·위기 이메일 sanitize·hw-gateway 내부 토큰) 도입 여부·우선순위 결정. CRITICAL 3건(web-backend F1/F2, notifier F1) 우선.
2. **재배포 후 최우선 확인 항목(0의 하위, §5b).** notifier 최후 보루 사슬(`/internal/alarms` 무인증 200 → admin WS `system_alarm` 브로드캐스트) 복구 여부를 재배포 직후 첫 번째로 확인. 이어 incident 멱등(alertId dedup 발화)·retained 드롭·타임라인 마커 순으로 재검증.
3. **mutating 계약 실측을 위한 스테이징/`ALLOW_MUTATING` 승인 범위 결정(§4).** 격리 DB 볼륨 + mock 장비/더미 RTMP 스택에서 mutating 레인을 어디까지 자동화할지, 침습 최상위(브로커 stop·컨테이너 재생성·물리 디바이스 RS-5)는 입회/스테이징으로 분리할지. 핵심 SKIPPED ≈136건의 실측 커버리지가 여기에 달림.
4. **admin fixture(`ADMIN_PASSWORD`) 주입으로 skip 해제(§4, §5e).** interface-web-api 11건 + web-backend 인증 GET 다수가 즉시 판정 가능해짐. seed admin 자격 주입 경로 확정. USER_TOKEN 주입 시 user-role 403 단언 9건 추가 해제.
5. **spec 결함 후보 정정 방향 확정(§6).** 특히 SD-mqtt-1(에러정책표 5xx 재시도)·SD-strm-1(startedAt 의미)·SD-cctv-1/SD-rec-1(reload 저하응답 teardown)은 안전·정합성에 직접 닿으므로 문서 정정과 함께 코드 가드(빈 목록 no-op, status code 검사) 도입 여부도 병행 판단.

추가 테스트 인프라 부채(§5e): RS-6 테스트 신규 작성, healthz 게이트 파싱 버그 수정, cctv 테스트 RTSP 소스 도입, web-frontend Playwright 세션 — 4건은 검증 커버리지 확대의 선결 과제.

---

[^hwgw]: 상세: `scratchpad/audit/hw-gateway.md` (단언 A~P/O2, finding F-1~F-7, SD-1/2)
[^cctv]: 상세: `scratchpad/audit/cctv-adapter.md` (단언 A~K, finding 1~8, SC1/SC2)
[^yt]: 상세: `scratchpad/audit/youtube-adapter.md` (단언 A~I, finding 1~7, SD-1~3)
[^strm]: 상세: `scratchpad/audit/streaming.md` (단언 A~H, finding F-1~F-7)
[^rec]: 상세: `scratchpad/audit/recording.md` (단언 A~O, finding 1~9)
[^notif]: 상세: `scratchpad/audit/notifier.md` (단언 A~J, finding 1~7, spec결함 1~3)
[^wb]: 상세: `scratchpad/audit/web-backend.md` (단언 A~S, finding F1~F8, spec결함 1~3)
[^wf]: 상세: `scratchpad/audit/web-frontend.md` (단언 A~N, finding 1~8, spec결함 1~5)
[^ifmqtt]: 상세: `scratchpad/audit/interface-mqtt.md` (단언 A/H/C/R/RS 22건, finding F-1~F-8, SD-1~3)
[^ifstrm]: 상세: `scratchpad/audit/interface-streaming.md` (단언 A1~A5 17건, finding 1~7, SD-1~3)
[^api]: 상세: `scratchpad/audit/interface-web-api.md` (계약 1~15 84건, finding F1~F7)
