# streaming 스펙

> 상태: 살아있는 계약 (living spec) · 독자: 스펙 작성자

## 목적 / 의도

- 시스템의 모든 영상 소스가 모이는 **중앙 HLS 스트리밍 허브**다. 어댑터들이 RTMP로 push한 스트림을 **remux만 수행**하여 HLS(m3u8 + ts)로 서빙한다. 제품 정의상 본 서비스가 "스트리밍 서버"이고 CCTV/YouTube는 소스를 흘려넣는 어댑터일 뿐이다 — 서버는 소스·벤더 독립적이어야 한다.
- **트랜스코딩을 절대 하지 않는다 (하드 불변식).** 단일 미니 PC에서 다수 카메라를 동시 처리하기 위해 CPU 사용을 최소로 유지하는 것이 핵심 의도다. 코덱을 검사하지 않으므로 적법한 H.264는 profile·B-frame 유무와 무관하게 통과·서빙한다. 코덱 정규화(비-H.264→H.264, 저지연용 B-frame 제거)는 어댑터 책임이며 허브로 새어들지 않는다.
- 스트림 alive/dead 상태의 **유일한 권위(SSOT)** 다. 다른 어떤 서비스도 스트림 상태를 보고하지 않으며, 상태 조회는 본 서비스의 HTTP API 하나로 수렴한다.
- 벤더 독립성: RTMP 입력 규격만 준수하면 어떤 카메라/어댑터든 교체 가능해야 하며, 그때 본 서비스는 변경되지 않는다.

## 언어 · 런타임

- 상태 API: **Go 1.22**, 표준 라이브러리만 사용 (외부 모듈 의존 0).
- 스트림 처리: **nginx + RTMP 모듈** (Alpine 패키지 `nginx-mod-rtmp`).
- 단일 컨테이너(Alpine 기반) 안에 두 프로세스가 동거한다: Go API는 백그라운드, nginx는 포그라운드(PID 1 계열). nginx가 죽으면 컨테이너가 죽는다.

## 의존 도구 · 시스템

- Docker 내부 네트워크만 사용한다. **어떤 포트도 호스트/외부에 직접 노출하지 않는다.** 클라이언트 접근은 web-frontend nginx의 `/live/` 프록시를 경유한다.
- 영구 저장소 없음. HLS 산출물 디렉토리(`/tmp/hls`)는 **tmpfs로 마운트**되어 컨테이너 재기동 시 항상 비워진다. 따라서 컨테이너 재생성(recreate)이든 단순 재시작(`docker compose restart`)이든 **모두 완전 초기화**된다 — 이전 산출물 잔존이 없어 재기동 거짓 alive(stale mtime으로 인한 `active:true` 오보)가 발생하지 않는다. 휘발성 계약의 소유자는 [docs/spec/interface-streaming.md](interface-streaming.md) §계약 4 "상태 휘발성"이다. 녹화는 본 서비스의 책임이 아니다 — 별도 서비스가 RTMP를 직접 구독하며, 그 접면은 같은 문서 §계약 3 (RTMP 라이브 재배포)이 소유한다.
- 다른 서비스에 대한 런타임 의존이 없다 (DB 미접속, MQTT 미접속). 본 서비스는 홀로 기동·판정한다.

## 입력

- **RTMP push** — `rtmp://streaming:1935/live/{streamKey}`. 코덱/컨테이너 입력 규격의 소유자는 [docs/spec/interface-streaming.md](interface-streaming.md) §계약 1 (RTMP 입력)이다(허브는 적법한 H.264를 B-frame 포함 여부와 무관하게 수용). 본 스펙은 규격을 재정의하지 않는다.
- **HTTP GET 요청** (내부 네트워크, 포트 8080 단일 진입점):
  - `/healthz` — 헬스체크 (compose가 호출)
  - `/api/streams` — 스트림 목록/상태 조회 (web-backend가 호출)
  - `/live/{streamKey}/index.m3u8`, `/live/{streamKey}/*.ts` — HLS 재생 (클라이언트)
- **환경변수** `STREAM_ACTIVE_TIMEOUT` (초, 양의 정수) — active 판정 창을 재정의한다. 미설정·파싱 실패·0 이하이면 기본 30초. 이 조정 노브(기본값 포함)의 계약 소유자는 [docs/spec/interface-streaming.md](interface-streaming.md) §계약 4다.

## 출력 (계약)

- **HLS 스트림** — `/live/{streamKey}/index.m3u8` + ts 세그먼트. URL 패턴·프래그먼트 길이·플레이리스트 길이·응답 헤더·MIME 타입·상대 URL 정책의 소유자는 [docs/spec/interface-streaming.md](interface-streaming.md) §계약 2 (HLS 라이브 출력)이다. 본 스펙은 그 값들을 재기술하지 않는다.
- **`GET /api/streams`** — 항상 HTTP 200 + JSON **배열**을 반환한다. 스트림이 하나도 없으면 빈 배열 `[]`이다 (null 아님, 에러 아님). 요소 필드(`cameraId`, `streamKey`, `hlsUrl`, `active`, `lastUpdatedAt`)의 의미와 active 판정 기준(판정 창 값 포함)의 소유자는 [docs/spec/interface-streaming.md](interface-streaming.md) §계약 4 (스트림 상태 SSOT)이다. 소비자는 "항목 부재"와 "`active:false`"를 동일 취급해야 한다(MUST — 같은 문서 §계약 4).
  - `hlsUrl`은 반드시 `/live/{streamKey}/index.m3u8` 형태의 **상대 경로**다. Docker 내부 호스트명이 노출되지 않는다. 절대 URL 조립은 호출자 몫.
  - `cameraId == streamKey` — RTMP 발행 URL의 경로 세그먼트가 그대로 두 필드에 들어간다.
- **`GET /healthz`** — HTTP 200 + JSON 본문 `{"status":"ok","service":"streaming"}`.
- 스트림 alive/dead 판정 결과 자체가 본 서비스의 출력 계약이다. 시스템 내 다른 어디에도 이 판정의 대체 소스가 없다.

## 핵심 로직 (동작)

1. nginx-rtmp가 1935/TCP로 RTMP를 수신하고, 스트림 키별 하위 디렉토리(nested)에 HLS 세그먼트를 기록한다. 오래된 세그먼트는 자동 삭제(cleanup)된다. 이 경로에서 코덱 변환은 일어나지 않는다.
2. HTTP 8080이 유일한 외부 접점이다. `/live/`(HLS 파일 서빙), `/healthz`, `/api/*` 모두 8080 하나로 수렴한다 — 통합자는 8080만 알면 된다.
3. `/api/streams`는 HLS 출력 디렉토리를 스캔한다. 각 하위 디렉토리 = 스트림 후보이며, 그 안에 `index.m3u8`이 존재하는 것만 목록에 포함된다 (플레이리스트 없는 디렉토리는 조용히 제외).
4. **active 판정**: 플레이리스트 파일의 최종 수정 시각이 판정 창 이내면 `active: true`, 그 외 `active: false`. 판정 창의 기본값·조정 규칙은 [docs/spec/interface-streaming.md](interface-streaming.md) §계약 4가 소유한다 (본 스펙은 값을 재기술하지 않음). 이것이 alive/dead SSOT의 구현 정의다.
5. `lastUpdatedAt`은 플레이리스트 최종 수정 시각(mtime)을 UTC RFC3339로 반환한다 — 시작 시각이 아니라 freshness 신호다. 라이브 중에는 폴링마다 전진한다.
6. HLS 디렉토리가 아예 없으면 (기동 직후 등) `/api/streams`는 에러 없이 빈 배열을 반환한다.
7. 컨테이너 기동 순서: HLS 디렉토리 생성 → Go API 백그라운드 기동 → nginx 포그라운드 기동. `/tmp/hls`가 tmpfs이므로 **재생성이든 재시작이든 기동 직후 HLS 디렉토리는 비어 있다** — 이전 스트림 흔적이 목록에 되살아나거나 stale mtime으로 `active: true`가 오보되는 일이 없다.

## 검증 단언 (TDD)

각 단언은 Docker 내부 네트워크(예: 임의의 인접 컨테이너)에서 실행하며 OK/NOK로 판정한다.

- A. **헬스**: `curl -fsS http://streaming:8080/healthz` 가 HTTP 200이고, 본문 JSON의 `status`가 `"ok"`다.
- B. **무스트림 빈 배열**: 발행 중인 스트림이 없는 상태(예: 컨테이너 재생성 직후)에서 `curl -fsS http://streaming:8080/api/streams` 가 HTTP 200이고 본문이 정확히 `[]`(JSON 빈 배열)다.
- C. **라이브 파이프라인**: 입력 규격을 준수하는 RTMP push(예: `ffmpeg -re -i sample.mp4 -c:v libx264 -bf 0 -g 30 -c:a aac -f flv rtmp://streaming:1935/live/spec-test` — `-g 30`은 키프레임 간격 요구(≤2초, interface-streaming §계약 1) 충족용. 미지정 시 libx264 기본 keyint 250으로 정적 소스에서 규격 위반)를 시작하고 10초 이내에 `curl -fsS http://streaming:8080/live/spec-test/index.m3u8` 가 HTTP 200이며, 본문이 `#EXTM3U`로 시작하고 `.ts` 세그먼트 항목을 1개 이상 포함한다.
- D. **active 판정 (SSOT)**: 단언 C의 push가 진행 중일 때 `/api/streams` 응답에 `streamKey == "spec-test"` 이고 `active == true` 인 항목이 존재한다. push를 중단하고 판정 창(값 소유: interface-streaming §계약 4, 기본 30초) + 여유 5초가 지난 뒤에는, 해당 항목이 목록에 없거나 있더라도 `active == false` 다.
- E. **상대 URL 정책**: `/api/streams` 응답의 모든 `hlsUrl`이 `/live/`로 시작하고 `index.m3u8`으로 끝나는 상대 경로이며, 스킴(`http://`)이나 호스트명을 포함하지 않는다. 또한 각 항목에서 `cameraId == streamKey` 다.
- F. **HLS 응답 규격**: 단언 C의 m3u8 응답이 [docs/spec/interface-streaming.md](interface-streaming.md) §계약 2의 단언 A2-1(헤더·Content-Type 판정)을 통과한다. 헤더 값은 그 단언이 소유하며 여기서 재정의하지 않는다.
- G. **휘발성 (재생성·재시작 균일)**: 스트림 발행 이력이 있는 상태에서 컨테이너를 **재생성**(`docker compose up -d --force-recreate streaming`)하든 **재시작**(`docker compose restart streaming`)하든, `/tmp/hls`가 tmpfs이므로 직후 `/api/streams`가 `[]`를 반환한다 (이전 스트림 잔재 없음). 어느 한쪽이라도 잔존 playlist로 항목이 남으면 NOK (tmpfs 미적용 신호). 재기동 거짓 alive 방지 계약은 §계약 4 A4-6이 소유한다.
- H. **B-frame 수용 (소스 독립성 회귀)**: B-frame이 포함된 RTMP push(예: `-bf 2 -profile:v main`)가 **지속 가능한 스트림을 만든다** — 60초 완주하며 조기 종료하지 않고, push 중 해당 스트림의 m3u8이 HTTP 200으로 서빙·갱신된다. 조기에 연결이 끊기면 NOK(허브가 적법한 H.264를 거부 = 소스 독립성 위반). 계약 방향의 소유자는 §계약 1 A1-3이다.

## 설계 결정 기록 (Resolved)

이전 "리뷰 필요" 항목은 원점 재검토로 확정되어 계약 본문에 반영됨. 상세 근거는 [docs/spec/interface-streaming.md](interface-streaming.md) "설계 결정 기록"이 SSOT다.

1. **`startedAt` → `lastUpdatedAt` 개명** — 값은 playlist mtime(freshness), 죽은 필드였음. 이름을 실제 의미로 정정, 시작 시각 추적은 미채택. (§계약 4)
2. **dead 스트림 잔존 + 소비자 부재==inactive(MUST)** — 잔존 수명은 불확정이나 소비자가 "부재"와 "`active:false`"를 동일 취급하면 무해. (§계약 4)
3. **HLS 지연 표기 통일** — "5~10초"(§계약 2 SSOT)로 통일. 본 서비스 문서·레거시 가이드의 "4~6초" 표기는 이 값으로 정정.
4. **CORS `*`는 의도된 개방** — 프론트 직접 재생 편의용, 내부망 전용이라 실해 없음. (§계약 2)
5. **tmpfs 휘발성 균일화** — `/tmp/hls` tmpfs 마운트로 재생성·재시작 모두 완전 초기화, 재기동 거짓 alive 제거. (§계약 4 "상태 휘발성", A4-6 / 본 문서 단언 G)
