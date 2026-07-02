# streaming 스펙

> 상태: 살아있는 계약 (living spec) · 독자: 스펙 작성자

## 목적 / 의도

- 시스템의 모든 영상 소스가 모이는 **중앙 HLS 스트리밍 허브**다. 어댑터들이 RTMP로 push한 스트림을 **remux만 수행**하여 HLS(m3u8 + ts)로 서빙한다.
- **트랜스코딩을 절대 하지 않는다.** 단일 미니 PC에서 다수 카메라를 동시 처리하기 위해 CPU 사용을 최소로 유지하는 것이 핵심 의도다.
- 스트림 alive/dead 상태의 **유일한 권위(SSOT)** 다. 다른 어떤 서비스도 스트림 상태를 보고하지 않으며, 상태 조회는 본 서비스의 HTTP API 하나로 수렴한다.
- 벤더 독립성: RTMP 입력 규격만 준수하면 어떤 카메라/어댑터든 교체 가능해야 하며, 그때 본 서비스는 변경되지 않는다.

## 언어 · 런타임

- 상태 API: **Go 1.22**, 표준 라이브러리만 사용 (외부 모듈 의존 0).
- 스트림 처리: **nginx + RTMP 모듈** (Alpine 패키지 `nginx-mod-rtmp`).
- 단일 컨테이너(Alpine 기반) 안에 두 프로세스가 동거한다: Go API는 백그라운드, nginx는 포그라운드(PID 1 계열). nginx가 죽으면 컨테이너가 죽는다.

## 의존 도구 · 시스템

- Docker 내부 네트워크만 사용한다. **어떤 포트도 호스트/외부에 직접 노출하지 않는다.** 클라이언트 접근은 web-frontend nginx의 `/live/` 프록시를 경유한다.
- 영구 저장소 없음. HLS 산출물은 컨테이너 쓰기 레이어의 내부 디렉토리에만 존재한다 (볼륨/tmpfs 마운트 없음). 따라서 **컨테이너를 재생성(recreate, 예: `down` 후 `up`)하면 전부 초기화**되지만, 컨테이너를 재생성하지 않는 재시작(`docker compose restart`)에서는 이전 산출물이 쓰기 레이어에 그대로 잔존한다. (→ 리뷰 필요 항목 6 참조) 녹화는 본 서비스의 책임이 아니다 — 별도 서비스가 RTMP를 직접 구독하며, 그 접면은 [docs/spec/interface-streaming.md](interface-streaming.md) §계약 3 (RTMP 라이브 재배포)이 소유한다.
- 다른 서비스에 대한 런타임 의존이 없다 (DB 미접속, MQTT 미접속). 본 서비스는 홀로 기동·판정한다.

## 입력

- **RTMP push** — `rtmp://streaming:1935/live/{streamKey}`. 코덱/컨테이너/B-frame 금지 등 입력 규격의 소유자는 [docs/spec/interface-streaming.md](interface-streaming.md) §계약 1 (RTMP 입력)이다. 본 스펙은 규격을 재정의하지 않는다.
- **HTTP GET 요청** (내부 네트워크, 포트 8080 단일 진입점):
  - `/healthz` — 헬스체크 (compose가 호출)
  - `/api/streams` — 스트림 목록/상태 조회 (web-backend가 호출)
  - `/live/{streamKey}/index.m3u8`, `/live/{streamKey}/*.ts` — HLS 재생 (클라이언트)
- **환경변수** `STREAM_ACTIVE_TIMEOUT` (초, 양의 정수) — active 판정 창을 재정의한다. 미설정·파싱 실패·0 이하이면 기본 30초. (→ 리뷰 필요 항목 1 참조)

## 출력 (계약)

- **HLS 스트림** — `/live/{streamKey}/index.m3u8` + ts 세그먼트. URL 패턴·프래그먼트 길이·플레이리스트 길이·응답 헤더·MIME 타입·상대 URL 정책의 소유자는 [docs/spec/interface-streaming.md](interface-streaming.md) §계약 2 (HLS 라이브 출력)이다. 본 스펙은 그 값들을 재기술하지 않는다.
- **`GET /api/streams`** — 항상 HTTP 200 + JSON **배열**을 반환한다. 스트림이 하나도 없으면 빈 배열 `[]`이다 (null 아님, 에러 아님). 요소 필드(`cameraId`, `streamKey`, `hlsUrl`, `active`, `startedAt`)의 의미와 active 판정 기준(판정 창 값 포함)의 소유자는 [docs/spec/interface-streaming.md](interface-streaming.md) §계약 4 (스트림 상태 SSOT)이다.
  - `hlsUrl`은 반드시 `/live/{streamKey}/index.m3u8` 형태의 **상대 경로**다. Docker 내부 호스트명이 노출되지 않는다. 절대 URL 조립은 호출자 몫.
  - `cameraId == streamKey` — RTMP 발행 URL의 경로 세그먼트가 그대로 두 필드에 들어간다.
- **`GET /healthz`** — HTTP 200 + JSON 본문 `{"status":"ok","service":"streaming"}`.
- 스트림 alive/dead 판정 결과 자체가 본 서비스의 출력 계약이다. 시스템 내 다른 어디에도 이 판정의 대체 소스가 없다.

## 핵심 로직 (동작)

1. nginx-rtmp가 1935/TCP로 RTMP를 수신하고, 스트림 키별 하위 디렉토리(nested)에 HLS 세그먼트를 기록한다. 오래된 세그먼트는 자동 삭제(cleanup)된다. 이 경로에서 코덱 변환은 일어나지 않는다.
2. HTTP 8080이 유일한 외부 접점이다. `/live/`(HLS 파일 서빙), `/healthz`, `/api/*` 모두 8080 하나로 수렴한다 — 통합자는 8080만 알면 된다.
3. `/api/streams`는 HLS 출력 디렉토리를 스캔한다. 각 하위 디렉토리 = 스트림 후보이며, 그 안에 `index.m3u8`이 존재하는 것만 목록에 포함된다 (플레이리스트 없는 디렉토리는 조용히 제외).
4. **active 판정**: 플레이리스트 파일의 최종 수정 시각이 판정 창 이내면 `active: true`, 그 외 `active: false`. 판정 창의 기본값·조정 규칙은 [docs/spec/interface-streaming.md](interface-streaming.md) §계약 4가 소유한다 (본 스펙은 값을 재기술하지 않음). 이것이 alive/dead SSOT의 구현 정의다.
5. `startedAt`은 플레이리스트 최종 수정 시각을 UTC RFC3339로 반환한다. (→ 리뷰 필요 항목 2 참조)
6. HLS 디렉토리가 아예 없으면 (기동 직후 등) `/api/streams`는 에러 없이 빈 배열을 반환한다.
7. 컨테이너 기동 순서: HLS 디렉토리 생성 → Go API 백그라운드 기동 → nginx 포그라운드 기동. 컨테이너를 **재생성**하면 이전 스트림 흔적은 남지 않는다. 재생성 없는 재시작에서는 직전 playlist가 잔존하여 목록에 다시 나타날 수 있으며, 직전까지 발행 중이던 스트림은 mtime이 판정 창 이내라 일시적으로 `active: true`로 보일 수 있다.

## 검증 단언 (TDD)

각 단언은 Docker 내부 네트워크(예: 임의의 인접 컨테이너)에서 실행하며 OK/NOK로 판정한다.

- A. **헬스**: `curl -fsS http://streaming:8080/healthz` 가 HTTP 200이고, 본문 JSON의 `status`가 `"ok"`다.
- B. **무스트림 빈 배열**: 발행 중인 스트림이 없는 상태(예: 컨테이너 재생성 직후)에서 `curl -fsS http://streaming:8080/api/streams` 가 HTTP 200이고 본문이 정확히 `[]`(JSON 빈 배열)다.
- C. **라이브 파이프라인**: 입력 규격을 준수하는 RTMP push(예: `ffmpeg -re -i sample.mp4 -c:v libx264 -bf 0 -c:a aac -f flv rtmp://streaming:1935/live/spec-test`)를 시작하고 10초 이내에 `curl -fsS http://streaming:8080/live/spec-test/index.m3u8` 가 HTTP 200이며, 본문이 `#EXTM3U`로 시작하고 `.ts` 세그먼트 항목을 1개 이상 포함한다.
- D. **active 판정 (SSOT)**: 단언 C의 push가 진행 중일 때 `/api/streams` 응답에 `streamKey == "spec-test"` 이고 `active == true` 인 항목이 존재한다. push를 중단하고 판정 창(값 소유: interface-streaming §계약 4, 기본 30초) + 여유 5초가 지난 뒤에는, 해당 항목이 목록에 없거나 있더라도 `active == false` 다.
- E. **상대 URL 정책**: `/api/streams` 응답의 모든 `hlsUrl`이 `/live/`로 시작하고 `index.m3u8`으로 끝나는 상대 경로이며, 스킴(`http://`)이나 호스트명을 포함하지 않는다. 또한 각 항목에서 `cameraId == streamKey` 다.
- F. **HLS 응답 규격**: 단언 C의 m3u8 응답이 [docs/spec/interface-streaming.md](interface-streaming.md) §계약 2의 단언 A2-1(헤더·Content-Type 판정)을 통과한다. 헤더 값은 그 단언이 소유하며 여기서 재정의하지 않는다.
- G. **휘발성**: 스트림 발행 이력이 있는 상태에서 컨테이너를 **재생성**(예: `docker compose up -d --force-recreate streaming`)하면, 직후 `/api/streams`가 `[]`를 반환한다 (이전 스트림 잔재 없음). 단순 재시작(`docker compose restart streaming`)은 쓰기 레이어를 보존하여 잔존 playlist가 목록에 남으므로 본 단언의 대상이 아니다.
- H. **규격 위반 입력의 조기 실패 (회귀)**: B-frame이 포함된 RTMP push(예: `-bf 2`)는 지속 가능한 스트림을 만들지 못한다 — push 시작 후 수 초 내 연결이 리셋되거나, 30초 시점에 해당 스트림의 m3u8이 정상 갱신되지 않는다. (규격 위반이 조용히 수용되지 않음을 확인)

## ⚠️ 리뷰 필요 (의도 불확실)

1. **`STREAM_ACTIVE_TIMEOUT` 환경변수의 존재** — 코드는 이 환경변수로 active 판정 창을 재정의할 수 있으나, 서비스 가이드는 "compose에서 별도 env 없음 (기본 포트/경로 하드코딩)"이라 하고, 인터페이스 SSOT는 30초를 고정값처럼 기술한다. 테스트용 뒷문인지 공식 조정 노브인지 불명확 — 계약에 포함할지 결정 필요.
2. **`startedAt`의 실제 의미** — 필드명과 인터페이스 문서의 예시는 "스트림 시작 시각"을 암시하지만, 구현은 플레이리스트 **최종 수정 시각**(mtime)을 반환한다. 즉 활성 스트림에서는 `startedAt`이 몇 초마다 계속 갱신된다. 필드명 변경 또는 시작 시각 추적 중 어느 쪽이 의도인지 확인 필요.
3. **종료된 스트림의 목록 잔존 수명** — push가 끝나도 플레이리스트 파일이 디스크에 남아 있는 동안 해당 스트림은 `active: false`로 목록에 계속 노출되며, 목록에서 사라지는 시점은 nginx의 자동 cleanup 타이밍에 의존한다. "dead 스트림도 목록에 남는다"를 계약으로 못 박을지, cleanup 후 제거를 보장할지 불명확.
4. **HLS 지연 수치 문서 불일치** — 서비스 가이드는 라이브 지연 "약 4~6초", 인터페이스 SSOT는 "일반적으로 5~10초"로 서로 다르게 기술한다. 코드 설정(프래그먼트 2초, 플레이리스트 10초)은 동일하므로 문서 간 표기 통일 필요.
5. **CORS 전면 개방** — `/live/` 응답에 `Access-Control-Allow-Origin: *`가 무조건 붙는다. 내부 네트워크 전용이라 실해는 없어 보이나, 의도(프론트엔드 직접 재생 편의)인지 잔재인지 어느 문서에도 명시가 없다.
6. **재시작(restart) 시 잔존 playlist** — HLS 디렉토리에 볼륨/tmpfs 마운트가 없어 `docker compose restart`(컨테이너 재생성 아님)에서는 직전 스트림의 playlist가 쓰기 레이어에 남아 `/api/streams` 목록에 다시 나타나며, 직전까지 발행 중이던 스트림은 mtime이 최근이라 일시적으로 `active: true`로 보고될 수 있다. "재시작에서도 완전 초기화"가 의도라면 tmpfs 마운트 등 보강이 필요 — 휘발성 보장 범위(재생성만 vs 재시작 포함)의 의도 확인 필요.
