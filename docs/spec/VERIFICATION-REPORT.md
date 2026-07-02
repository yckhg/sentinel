# Spec 정합성 검증 리포트 (2026-07-02)

> 대상: docs/spec/ 초판 11종 (서비스 8 + 인터페이스 3)
> 방법: 접면별 독립 검증 에이전트 3개 (MQTT / Streaming / Web API).
> 각 모순은 실제 코드를 근거로 어느 쪽이 맞는지 판정함. 작성자와 검증자는 분리.

## A. 시스템 실결함 (spec 문제가 아니라 코드가 어긋난 곳 — 수정 백로그 후보)

| # | 발견 | 판정 근거 |
|---|------|-----------|
| A-1 | **notifier의 최후 보루 `POST /api/alarms` 수신 라우트가 web-backend에 없음.** `/api/` 프리픽스라 인증 미들웨어에 걸려 항상 401 → "외부 채널 전무 시 시스템 알람 1건 보장" 계약이 실환경에서 항상 실패, 로그만 남음 | notifier main.go:393 호출 vs web-backend 라우팅 전수 확인 (부재) |
| A-2 | **hw-gateway가 incident forward 시 `alertId`를 페이로드에 넣지 않음** → web-backend에 실재하는 DB unique 인덱스 기반 멱등 경로가 영원히 발화 불가. 멱등이 hw-gateway in-memory dedup(재시작 시 소실)에만 의존 | hw-gateway IncidentPayload 필드 vs web-backend incidents.go:43-60 + migrations.go unique index |
| A-3 | 프론트 타임라인 사고 마커가 `data.incidents`를 읽으나 API는 `data` 반환 → 마커 영구 미렌더 | RecordingTimeline.tsx:144 vs incidents.go:246 |
| A-4 | hw-gateway 재시도는 transport 에러만 재시도, HTTP 4xx/5xx 응답은 미재시도 — 어떤 문서도 이 구분 미명시 | forwardToWebBackend 로직 |
| A-5 | 센서 생존 판정 이원화: 프론트 장비 탭 30초 하드코딩 vs health API 설정값 기본 60초 — 사용자에게 상이한 두 판정 공존 | DevicesSection.tsx:17 vs health.go:222 |

## B. Spec 자체 결함 — 심각 (본문 수정 필요)

### 참조 그래프 (구조적, 3접면 공통)
- **B-1**: 서비스 spec들이 접면 소유자로 전부 레거시 `docs/interfaces/*.md`를 지목. 새 `docs/spec/interface-*.md`를 참조하는 spec이 0개 (병렬 작성의 예상된 부작용). SSOT 승격 결정 + 포인터 일괄 교체 필요.
- **B-2**: recording의 RTMP pull 계약이 인용한 레거시 문서에 없음 (새 interface-streaming §계약 3에만 존재) — B-1 해소 시 자동 해소.

### interface-mqtt.md (패턴: ⚠️ 섹션에서 코드 불일치를 자인했으나 본문은 이상형 유지)
- **B-3**: alert 토픽의 실제 발행자(Sentinel발 테스트 alert, alertId 없음)를 본문이 부정. "발행 2토픽"도 실제 3토픽과 불일치.
- **B-4**: restart 등록 게이트를 잘못된 계층에 귀속 — 게이트는 web-backend에만 있고 hw-gateway `/api/restart`는 미등록 device도 그대로 발행.
- **B-5**: heartbeat `status`/`alertState` 필수 표기 vs 코드는 관용(기본값 보정). candidate `confidence 0.0` 유효 표기 vs 코드는 양수만 수용.

### interface-web-api.md
- **B-6**: `POST /api/incidents` 멱등 계약(alertId dedup + deviceId upsert)이 본문 누락 — web-backend.md와 바디 스키마가 다름.
- **B-7**: 계약 13 표에 `/internal/contacts` 누락 + 자기 ⚠️1과 자기모순("반영했다"고 주장하나 미반영).
- **B-8**: `/internal/cameras` 응답 스키마 미정의, 호출자 4곳(cctv/youtube/notifier/recording) 중 1곳만 기재.
- **B-9**: hw-gateway inbound HTTP API(`/api/restart`, `/api/test-alert`, `/api/alert/resolved`, `/api/equipment/status`)의 spec-레벨 인터페이스 문서 부재 (web-backend가 실호출 확인됨).

### interface-streaming.md
- **B-10**: 단언 A4-3이 "40초 후 항목 잔존 + active:false"를 전제하나 nginx cleanup으로 항목이 소멸 가능 — 위양성 NOK 위험. streaming.md 단언 D 쪽이 코드 부합.
- **B-11**: H.264 profile 계약(Baseline/Main)이 youtube-adapter에서 우연으로만 충족(`-preset ultrafast` 부작용) — push측 `-profile:v` pin 또는 계약 완화 중 택일 필요.

### notifier.md
- **B-12**: 존재하지 않는 수신측(`/api/alarms`) 계약을 자체 정의 (A-1의 spec측 면).

## C. 경미 (표현·중복·소량 침투)

- streamKey 형식 표기 불일치(`cam-{8hex}` vs 예시 `cam-001`), HLS 지연 수치 표기차(4~6s vs 5~10s — 둘 다 추정치)
- 값 중복 기술: streaming.md HLS 헤더, hw-gateway QoS 표 — SSOT 변경 시 이중 수정 필요
- 본문 구현 세부 소량 침투: 로그 문자열을 판정 기준으로 고정(interface-mqtt), `authMiddleware`·goroutine·버퍼 크기 등 (전 문서에서 구현 경로/버그 이력/수정 지시의 본문 침투는 0건 — ⚠️ 격리 규율 준수 양호)
- 죽은 계약: heartbeat optional `alertId` — 정의만 있고 소비자·코드 사용처 없음
- `alertState`가 `/api/devices/seen`으로 전달되나 alert 경로에서 "none" 고정 + web-backend측 소비 계약 부재

## D. 각 서비스 spec의 ⚠️ 리뷰 필요 항목

각 spec 파일 말미 "⚠️ 리뷰 필요" 섹션 참조 (총 65건). 우선 리뷰 권고:
1. **notifier**: `KAKAO_ENABLED`/`SMS_ENABLED` 기본 꺼짐 — 미설정 시 외부 알림 전부 비활성
2. **web-backend 보안 묶음**: 임시 링크 발급 무인증 가능성, temp 역할 권한 초과, 폐기 링크 JWT 우회 가능성, SSRF 도메인 우회, X-Forwarded-For 신뢰
3. **hw-gateway**: dedup이 forward 성공 전 등록 → 전량 실패 시 재전송으로도 복구 불가
4. **recording**: 타임존 없는 시간 UTC 해석(KST 시 보호 구간 9h 오차), 재시작 시 미완료 아카이브 영구 방치

## 후속 결정 대기

1. `docs/spec/interface-*.md`의 SSOT 승격 여부 (승격 시 서비스 spec 포인터 교체 + 레거시 `docs/interfaces/` 처리 방침)
2. interface 본문의 "이상형 vs 코드 실측" 노선 — 검증 단언이 현재 코드에서 OK가 되도록 실측 기준으로 통일할지
3. A절 시스템 실결함 5건의 수정 백로그화
