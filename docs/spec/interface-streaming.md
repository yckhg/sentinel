# 스트리밍 인터페이스 스펙

> 상태: 살아있는 계약 (living spec) · 독자: 스펙 작성자 / 오케스트레이터
>
> 이 문서는 Sentinel 스트리밍 접합부(seam)의 SSOT입니다. 개별 서비스 스펙은 이 문서의 계약을 참조하며, 계약 변경 시 관련 서비스 스펙·코드와 같은 커밋에서 수정합니다. (`docs/interfaces/streaming-api.md`를 계약 + 검증 단언 형태로 승격한 문서)

## 목적 / 의도

- **단일 수신·단일 송출**: 모든 영상 소스는 RTMP push로 streaming 서비스에 모이고, 클라이언트는 HLS로만 시청한다. 소스(카메라/어댑터)와 시청자(웹) 사이의 결합을 이 접합부 하나로 끊는다.
- **무 트랜스코딩 (remux only) — 하드 불변식**: streaming 서비스는 코덱을 **절대** 건드리지 않는다. FLV→TS 컨테이너 변환만 수행하여 mini PC 한 대에서 다중 스트림을 처리한다. remux-only는 CPU를 스트림 수에 대해 상수로 유지하는 유일한 방법이며, 이것이 제품 코어(단일 온프레미스 PC, 저지연)를 성립시킨다.
- **코덱 정규화는 어댑터 책임 (아키텍처 원칙)**: 소스 코덱을 브라우저 재생 규격(H.264/AAC)에 맞추는 모든 정규화 — 비-H.264(HEVC 등) → H.264 트랜스코딩, 저지연이 필요한 경우의 B-frame 제거 — 는 **push 측(어댑터)의 책임**이다. 허브는 어떤 이유로도 트랜스코딩하지 않는다. 이 원칙은 향후 HEVC 카메라 등장 등으로 트랜스코딩이 허브로 새어드는 것을 사전 차단한다. (youtube-adapter의 상시 재인코딩은 이 원칙의 예외가 아니라 **정규 적용 사례**다.)
- **소스 독립성 (벤더 독립성)**: 이 스펙만 준수하면 어떤 카메라/어댑터든 교체 가능. streaming·web-backend는 변경 없이 유지된다. 허브는 적법한 H.264를 소스 특성(profile, B-frame 유무)과 무관하게 수용해야 하며, 적법한 입력을 거부하는 것은 제품 목적(소스 독립) 위반이다.
- **상태 권위 단일화**: 스트림 alive/dead 판정의 유일한 권위는 streaming 서비스. 어댑터·타 서비스는 스트림 상태를 보고하지 않는다.

## 언어 · 런타임

| 역할 | 기술 |
|------|------|
| RTMP 수신 / HLS 생성·서빙 | nginx + ngx_rtmp_module (v1.2.2) |
| 상태 API (`/api/streams`, `/healthz`) | Go (표준 라이브러리 net/http) |
| 배포 형태 | 단일 Docker 컨테이너 (프로젝트 내부 네트워크 `sentinel-net` 전용 — 런타임 이름 `sentinel_sentinel-net`. 외부 공유 네트워크 `yc-network`에는 속하지 않으며, yc-network에서 호스트명 `streaming`은 해석되지 않는다), 메모리 상한 256M |

외부 통합자 관점에서는 **RTMP 1935 포트, HTTP 8080 포트** 두 개만 존재한다. (컨테이너 내부의 프로세스 분리는 계약 대상이 아님)

## 의존 도구 · 시스템

- **FFmpeg** — 어댑터의 RTMP push, recording의 RTMP pull, 검증 단언 실행(ffprobe)에 사용
- **hls.js** — 브라우저 측 HLS 재생 (web-frontend)
- **Docker 내부 네트워크 (`sentinel-net`)** — RTMP/HTTP 모두 내부 전용. 외부 노출은 web-frontend nginx가 `/live/`를 streaming:8080으로 프록시하는 경로뿐 (web-frontend만 `yc-network`에 추가로 조인)
- **호스트 이름 계약**: 컨테이너 이름 `streaming`이 주소의 일부다 (`rtmp://streaming:1935/...`, `http://streaming:8080/...`)

---

## 계약 1: RTMP 입력 (어댑터/카메라 → streaming)

### 입력

| 항목 | 요구사항 |
|------|----------|
| Protocol | RTMP (push) |
| Endpoint | `rtmp://streaming:1935/live/{streamKey}` (내부 네트워크 전용) |
| Container | FLV (`-f flv`) |
| Video codec | **H.264 (모든 profile — Baseline/Main/High 수락)**. profile을 강제하지 않는다. 비-H.264(HEVC 등)는 브라우저 HLS 재생이 불가하므로 어댑터가 H.264로 정규화해 push한다(허브는 트랜스코딩하지 않음 — 코덱 정규화 원칙). |
| Audio codec | AAC (LC, HE-AAC 등 모든 profile 허용) |
| **B-frames** | **수용됨 — 거부하지 않는다.** 허브는 remux-only라 코덱을 검사하지 않으며, 적법한 H.264(B-frame 포함)를 통과·서빙한다. B-frame은 프레임 재정렬로 지연을 늘리므로, **저지연이 필요한 어댑터는** push 측에서 `-bf 0`으로 제거할 수 있다(**선택 권고이며 계약 위반이 아니다**). 특정 다운스트림 소비자가 B-frame에서 실제로 깨진다면 그것은 그 소비자의 국소 제약으로 문서화할 뿐, 전역 입력 계약으로 올리지 않는다. |
| **키프레임 간격** | **최대 2초 권고** — streaming은 키프레임에서만 HLS fragment를 자르므로(계약 2), 소스 키프레임 간격이 2초를 넘으면 세그먼트 길이·초기 재생 지연이 그만큼 늘어난다. 위반해도 push는 수락되며(연결 거부 없음) 계약 2의 fragment/지연 수치만 보장되지 않는다. 재인코딩 시 `-g <2×fps>` 지정 권장. |
| streamKey | 영숫자/하이픈 권장, 슬래시 금지. 형식은 강제되지 않음 — `cam-{8hex}`는 web-backend가 카메라 생성 시 발급하는 **생성 기본값(권장)**이며, 다른 형식(예: `yt-cam-1`)도 실존·수락된다 |

### 출력 (계약)

- push가 수락되면 streaming이 해당 streamKey의 HLS 스트림(계약 2)과 상태 항목(계약 4)을 자동 생성한다. 어댑터가 추가로 할 일은 없다.
- push 등록 API 없음 — RTMP 연결 자체가 등록이다.

### 핵심 로직 (동작 — 불변식)

- **허브 무검사 불변식**: 허브는 push된 스트림의 코덱을 검사하지 않고 통과·서빙한다. 적법한 H.264(B-frame·profile 무관)는 조기 종료 없이 지속된다. 따라서 push 지속을 위해 소스 측이 보장해야 하는 코덱 제약은 없다.
- **copy 우선 원칙**: 소스가 이미 H.264 + AAC면 어댑터는 `-c copy`로 push한다 (RTSP 어댑터 표준 패턴). 재인코딩은 코덱 정규화(비-H.264 → H.264)나 저지연을 위한 B-frame 제거 등 **어댑터 판단에 따른 선택**이며, 허브 계약 준수를 위해 강제되는 것은 아니다.
- **저지연 권고**: B-frame·큰 키프레임 간격은 지연을 늘린다. 저지연이 필요한 어댑터는 `-tune zerolatency -bf 0`, `-g <2×fps>`를 push 측에서 적용할 수 있다(선택). 이는 correctness 요구가 아니라 지연 최적화다.
- streamKey는 push URL의 마지막 path segment이며, 이후 모든 산출물(HLS 경로, 상태 API의 `streamKey`/`cameraId`)의 식별자가 된다.

권장 push 커맨드 (저지연/정규화가 필요한 경우 — 선택):

```bash
ffmpeg -i <source> \
  -c:v libx264 -tune zerolatency -bf 0 -g 30 \
  -c:a aac \
  -f flv rtmp://streaming:1935/live/cam-3f2a9b1c
```

(`-bf 0`/`-g 30`은 저지연 최적화 선택지다 — 허브 수락 조건이 아니다. `-g 30`은 15fps 기준 키프레임 간격 2초로, 소스 fps에 맞춰 `2×fps`로 조정한다. 소스가 이미 H.264 + AAC면 `-c copy`가 우선.)

(예시의 `cam-3f2a9b1c`는 web-backend 생성 기본값 형식 `cam-{8hex}`를 따른 것 — 형식 자체는 강제되지 않는다.)

### 검증 단언 (TDD)

- **A1-1 (정상 push 지속)**: 규격 준수 테스트 스트림을 60초 push했을 때 FFmpeg가 조기 종료하지 않으면 OK.
  ```bash
  docker run --rm --network sentinel_sentinel-net linuxserver/ffmpeg \
    -f lavfi -i testsrc=size=640x360:rate=15 -f lavfi -i sine \
    -t 60 -c:v libx264 -tune zerolatency -bf 0 -g 30 -c:a aac \
    -f flv rtmp://streaming:1935/live/spec-test-a1
  # exit code 0 → OK / 60초 이전 비정상 종료 → NOK
  ```
- **A1-2 (HLS 자동 생성)**: A1-1 push 시작 후 10초 이내에 `curl -sf http://streaming:8080/live/spec-test-a1/index.m3u8`이 HTTP 200 + `#EXTM3U`로 시작하는 본문을 반환하면 OK.
- **A1-3 (B-frame 수용 — 소스 독립성 계약)**: 동일 push를 `-bf 2`(B-frame 포함, `-profile:v main`)로 실행하면 **60초 완주하며 조기 종료하지 않고**, push 중 `curl -sf http://streaming:8080/live/spec-test-a1/index.m3u8`이 HTTP 200 + `#EXTM3U`를 반환하고 갱신되면 OK. 즉 허브는 B-frame을 통과·서빙한다. **조기에 연결이 끊기면 NOK**(허브가 적법한 H.264를 거부 → 소스 독립성 계약 위반 → 스펙/코드 재검토 트리거).
  ```bash
  docker run --rm --network sentinel_sentinel-net linuxserver/ffmpeg \
    -f lavfi -i testsrc=size=640x360:rate=15 -f lavfi -i sine \
    -t 60 -c:v libx264 -profile:v main -bf 2 -g 30 -c:a aac \
    -f flv rtmp://streaming:1935/live/spec-test-bf
  # exit code 0 (60초 완주) + push 중 m3u8 200 → OK / 5초 내 broken pipe·EOF 조기 종료 → NOK
  ```

---

## 계약 2: HLS 라이브 출력 (streaming → 브라우저)

### 입력

- HTTP GET `/live/{streamKey}/index.m3u8` 및 하위 `.ts` 세그먼트 (streaming:8080)
- 브라우저는 이 경로를 직접 알지 못하며, web-backend가 중계한 상대 URL(계약 4)로만 접근한다. 외부 노출 경로: web-frontend nginx가 `/live/` → `streaming:8080` 프록시.

### 출력 (계약)

| 항목 | 값 |
|------|-----|
| Format | HLS (`m3u8` playlist + `ts` 세그먼트, nested 디렉토리) |
| URL pattern | `/live/{streamKey}/index.m3u8` (**상대 경로**) |
| Fragment duration | **max(2초, 소스 키프레임 간격)** — streaming은 키프레임에서만 fragment를 자른다. 소스가 계약 1의 키프레임 간격 요구(≤2초)를 지키면 2초 |
| Playlist length | 10초 (2초 세그먼트 기준 5개) |
| Content-Type | `application/vnd.apple.mpegurl` (m3u8), `video/mp2t` (ts) |
| 응답 헤더 | `Cache-Control: no-cache`, `Access-Control-Allow-Origin: *` (프론트 직접 재생 편의용 · 내부망 전용이므로 실해 없음 — 의도된 개방) |
| Cleanup | 자동 (오래된 `.ts` 자동 제거) |
| Latency | 일반적으로 5~10초 (HLS 특성상 계약 위반 아님) — 소스 키프레임 간격 ≤2초 전제. 간격이 크면 첫 fragment 완결까지 m3u8 생성이 지연되어 초기 지연이 키프레임 간격만큼 늘어난다 |

### 핵심 로직 (동작 — 불변식)

- **무변환 불변식**: HLS 세그먼트의 비디오/오디오 코덱·해상도·프로파일은 RTMP로 push된 원본과 동일하다 (remux only, 트랜스코딩 없음).
- **상대 URL 정책**: 클라이언트에 전달되는 모든 스트림 URL은 `/live/...` 형태의 상대 경로. Docker 내부 주소(`http://streaming:8080/...`) 노출 금지.
- **세그먼트 회전 불변식**: 라이브 중 playlist는 계속 갱신되고(mtime 전진), 오래된 세그먼트는 디스크에서 제거되어 디스크 사용량이 스트림당 상수로 유지된다. (playlist에 나열되는 세그먼트 수 ≠ 디스크상 `.ts` 수 — 클린업은 playlist 창보다 큰 여유를 유지한다. 디스크 보존 상한은 A2-4 참조.)

### 검증 단언 (TDD)

- **A2-1 (playlist 형식/헤더)**: push 중
  ```bash
  curl -si http://streaming:8080/live/{streamKey}/index.m3u8
  ```
  → HTTP 200, `Content-Type: application/vnd.apple.mpegurl`, `Cache-Control: no-cache`, `Access-Control-Allow-Origin: *`, 본문 첫 줄 `#EXTM3U`이면 OK.
- **A2-2 (세그먼트 길이)**: 계약 1의 키프레임 간격 요구(≤2초)를 지키는 push(A1-1 테스트 스트림 등)에서 playlist의 `#EXTINF` 값이 2.0초 ±0.5 범위, 세그먼트 수 ≤ 6이면 OK. (키프레임 간격을 위반하는 소스에는 적용하지 않는다 — fragment 계약이 max(2초, 키프레임 간격)이므로)
- **A2-3 (무변환)**: 원본을 `h264 baseline 640x360`으로 push한 뒤
  ```bash
  ffprobe -v error -show_entries stream=codec_name,profile,width,height \
    http://streaming:8080/live/{streamKey}/index.m3u8
  ```
  → `codec_name=h264`, 해상도·프로파일이 push 원본과 동일하면 OK. 다르면 트랜스코딩이 발생한 것 → NOK.
- **A2-4 (세그먼트 자동 정리)**: 5분 push 후 스트림 디렉토리의 `.ts` 파일 수가 디스크 보존 상한(**≤ 24개**) 이내로 유지되면 OK. 단조 증가하면 NOK.
  - 근거: 디스크 보존량은 playlist 길이(5개)가 아니라 nginx-rtmp `hls_cleanup`의 age 기준으로 결정된다 — 클린업은 age < 2×`hls_playlist_length`(=20초)인 `.ts`만 남긴다. `hls_fragment 2초` 기준 10개이나, fragment는 키프레임에서 잘려 길이가 2초 미만으로 지터되므로 파일 수가 늘어 실측 12~20개다. 진행 중 세그먼트·타이밍 여유를 포함한 상한을 24개로 둔다.
- **A2-5 (재생 지연)**: 키프레임 간격 요구를 지키는 push(A1-1 테스트 스트림 등)에서, push 시작 시점의 실제 콘텐츠가 HLS로 시청 가능해지기까지 15초 이내면 OK.

---

## 계약 3: RTMP 라이브 재배포 (streaming → recording)

### 입력

- RTMP GET(play): `rtmp://streaming:1935/live/{streamKey}` — push 중인 스트림을 내부 서비스가 pull

### 출력 (계약)

- push 중인 원본과 동일한 코덱의 라이브 RTMP 스트림 (무변환 relay)
- push가 없는 streamKey를 pull하면 데이터 없이 대기/실패 (에러 아님 — 소비자가 재시도 책임을 진다)

### 핵심 로직 (동작 — 불변식)

- nginx-rtmp `live` 모드의 표준 동작으로, 동일 application 내 subscriber에게 push 스트림을 그대로 전달한다.
- 현재 유일한 내부 소비자는 recording 서비스 (녹화용 `-c copy` pull). **HLS pull(계약 2)과 달리 브라우저·외부에는 절대 노출하지 않는다** — 내부 서비스 간 전용 경로.
- 소비자가 붙거나 떨어져도 push 측·HLS 출력·상태 판정(계약 4)에 영향이 없어야 한다.

### 검증 단언 (TDD)

- **A3-1 (pull 가능)**: push 중
  ```bash
  ffprobe -v error -show_entries stream=codec_name \
    rtmp://streaming:1935/live/{streamKey}
  ```
  → 스트림 정보(h264 등)가 반환되면 OK.
- **A3-2 (소비자 무간섭)**: RTMP pull 세션을 붙였다 떼어도 같은 streamKey의 HLS playlist 갱신(A2-1)과 `/api/streams`의 `active: true`(A4-2)가 유지되면 OK.

---

## 계약 4: GET /api/streams — 스트림 상태 SSOT

### 입력

- HTTP GET `http://streaming:8080/api/streams` (인증 없음, 내부 네트워크 전용)
- 유일한 정규 소비자: web-backend (브라우저에 URL/상태를 중계). 다른 서비스가 스트림 상태를 자체 판정·보고하는 것은 계약 위반.

### 출력 (계약)

- HTTP 200, `Content-Type: application/json`, 스트림 항목 배열:

```json
[
  {
    "cameraId": "cam-3f2a9b1c",
    "streamKey": "cam-3f2a9b1c",
    "hlsUrl": "/live/cam-3f2a9b1c/index.m3u8",
    "active": true,
    "lastUpdatedAt": "2026-04-13T09:00:00Z"
  }
]
```

| 필드 | 계약 |
|------|------|
| `cameraId` | RTMP push URL의 `{streamKey}`와 동일 값 |
| `streamKey` | `cameraId`와 항상 동일 |
| `hlsUrl` | 상대 경로, 정확히 `/live/{streamKey}/index.m3u8` |
| `active` | boolean — 아래 판정 기준 |
| `lastUpdatedAt` | RFC3339 UTC 타임스탬프 — 해당 스트림 playlist의 **최종 갱신 시각(mtime)**. 라이브 중에는 폴링마다 전진한다. **시작 시각이 아니다.** 소비자는 이를 freshness 신호로 사용할 수 있다(`active` 판정의 근거값). 시스템 내 실 소비자는 현재 없다 |

- 스트림이 하나도 없으면 빈 배열 `[]` (200, null 아님)

### 핵심 로직 (동작 — 불변식)

- **활성 판정 기준 (SSOT)**: 마지막 30초 이내에 해당 스트림의 playlist가 갱신되었으면(`lastUpdatedAt`이 판정 창 이내) `active: true`, 그 외 `false`. (판정 창은 환경변수 `STREAM_ACTIVE_TIMEOUT`(초)으로 조정 가능, 기본 30)
- 판정 근거는 streaming 내부 관측(HLS 산출물 갱신)뿐이다. 어댑터의 자기 보고를 절대 반영하지 않는다.
- **소비자 의무 (MUST)**: dead 스트림은 HLS 산출물이 자동 정리(cleanup)될 때까지 `active: false` 항목으로 목록에 잔존할 수 있고, 정리 후에는 항목 자체가 소멸한다(수명 불확정). 따라서 **소비자는 "항목 부재"와 "`active: false`"를 동일하게 취급해야 한다(= disconnected).** 두 상태의 관측 결과는 항상 같아야 하며, 어느 쪽인지에 의존하는 로직을 두지 않는다.
- **휘발성 계약 (거짓 alive 금지)**: streaming의 HLS 산출물은 휘발성으로 다룬다(tmpfs 마운트 — 상세는 아래 "상태 휘발성"). 그 결과 **어떤 재기동(컨테이너 recreate·`docker compose restart` 모두)에서도** 이전 스트림 흔적이 남지 않는다. 재기동 직후 실제로 발행 중이지 않은 스트림이 stale mtime 때문에 일시적으로 `active: true`로 보고되는 일(거짓 alive)이 없어야 한다.
- web-backend는 이 API 하나만 호출하면 카메라별 시청 URL과 상태를 얻는다 — streaming의 다른 내부 사정을 알 필요가 없다.

### 상태 휘발성 (계약)

- streaming의 HLS 출력 디렉토리(`/tmp/hls`)는 **tmpfs로 마운트**되어 컨테이너 재기동 시 항상 비워진다. 이로써 recreate와 restart의 동작이 균일해진다 — 둘 다 완전 초기화.
- 근거: 마운트가 없으면 `docker compose restart`(컨테이너 재생성 아님)는 직전 playlist를 쓰기 레이어에 남겨, 재접속 중이라 실제로는 dead인 스트림을 최근 mtime으로 인해 `active: true`로 잘못 보고할 수 있다(SSOT 거짓 양성). tmpfs는 이 거짓 alive를 원천 제거한다.

### 검증 단언 (TDD)

- **A4-1 (빈 상태)**: 어떤 push도 없는 초기 상태에서 `curl -sf http://streaming:8080/api/streams` → `[]` 이면 OK.
- **A4-2 (활성 반영)**: `spec-test-a1` push 시작 10초 후 응답에 `{"cameraId":"spec-test-a1","streamKey":"spec-test-a1","hlsUrl":"/live/spec-test-a1/index.m3u8","active":true,...}` 항목이 있으면 OK.
  ```bash
  curl -s http://streaming:8080/api/streams | \
    jq -e '.[] | select(.streamKey=="spec-test-a1") | .active == true and .hlsUrl == "/live/spec-test-a1/index.m3u8" and .cameraId == .streamKey'
  ```
- **A4-3 (비활성 전환)**: push 중단 후 40초(판정 창 30초 + 여유) 뒤, 해당 streamKey 항목이 **목록에 없거나**(HLS 산출물 자동 정리로 항목 자체가 소멸할 수 있음), **있으면 `active`가 `false`**면 OK. 항목이 존재하면서 `active: true`면 NOK.
  ```bash
  curl -s http://streaming:8080/api/streams | \
    jq -e '[.[] | select(.streamKey=="spec-test-a1" and .active==true)] | length == 0'
  ```
- **A4-4 (상대 URL 정책)**: 모든 항목의 `hlsUrl`이 정규식 `^/live/[^/]+/index\.m3u8$`에 매치하고 `http`를 포함하지 않으면 OK.
  ```bash
  curl -s http://streaming:8080/api/streams | \
    jq -e 'all(.[]; .hlsUrl | test("^/live/[^/]+/index\\.m3u8$"))'
  ```
- **A4-5 (lastUpdatedAt 형식·의미)**: 모든 항목의 `lastUpdatedAt`이 RFC3339 UTC(`...Z`)로 파싱되면 OK. 또한 응답에 `startedAt` 키가 **존재하지 않으면** OK(개명 완료 확인 — 옛 이름 잔존 시 NOK).
  ```bash
  curl -s http://streaming:8080/api/streams | \
    jq -e 'all(.[]; (.lastUpdatedAt | test("Z$")) and (has("startedAt") | not))'
  ```
- **A4-6 (재기동 거짓 alive 금지 — 휘발성 SSOT)**: 스트림을 발행해 `active: true`가 관측된 뒤 push를 유지한 채 `docker compose restart streaming`을 실행하면(어댑터는 재접속 시도 중), 재기동 직후(어댑터 RTMP 재연결·첫 playlist 갱신 이전 창) `/api/streams`에 직전 streamKey가 `active: true`로 남아 있지 않으면 OK — 항목이 없거나 `active: false`여야 한다. stale mtime으로 `active: true`가 관측되면 NOK(거짓 alive — tmpfs 미적용 신호).
  ```bash
  curl -s http://streaming:8080/api/streams | \
    jq -e '[.[] | select(.streamKey=="spec-test-a1" and .active==true)] | length == 0'
  ```

---

## 계약 5: GET /healthz — 헬스체크

### 입력

- HTTP GET `http://streaming:8080/healthz` (인증 없음)

### 출력 (계약)

- HTTP 200, `Content-Type: application/json`, 본문 `{"status":"ok","service":"streaming"}`

### 핵심 로직 (동작 — 불변식)

- 200 응답은 "HTTP 진입점(8080)과 상태 API 프로세스가 살아 있음"을 의미한다. 개별 스트림의 활성 여부와 무관하다 (스트림 0개여도 healthy).
- Docker healthcheck가 이 엔드포인트를 주기 호출한다 — 응답 지연은 컨테이너 unhealthy 판정으로 이어진다.

### 검증 단언 (TDD)

- **A5-1**: `curl -sf http://streaming:8080/healthz` → HTTP 200, `jq -e '.status=="ok" and .service=="streaming"'` 통과하면 OK.
- **A5-2 (스트림 무관)**: push가 하나도 없는 상태에서도 A5-1이 통과하면 OK.

---

## 설계 결정 기록 (Resolved)

원점 재검토(제품 정의 = "스트리밍 서버, CCTV는 어댑터")로 확정된 결정. 이전의 "리뷰 필요" 항목을 계약 본문에 반영하고 여기 근거를 남긴다.

1. **B-frame은 수용 대상 (거부 아님)** — 허브는 remux-only라 코덱을 검사하지 않으며 적법한 H.264(B-frame 포함)를 통과·서빙한다(실측 확인: `-bf 2 -profile:v main` push 60초 완주 + active m3u8 서빙). 원 스펙의 "허브가 B-frame FLV를 ~5초 후 종료" 전제는 사실 오류이자 제품 목적(소스 독립성) 위반 방향이었다. 계약 1에서 "push측 금지"를 **저지연 선택 권고**로 강등. A1-3은 "거부 확인"에서 "수용 확인"으로 방향을 뒤집었다.
2. **코덱 정규화는 어댑터 책임** — 비-H.264(HEVC 등)→H.264, 저지연용 B-frame 제거는 push 측(어댑터)이 수행하고 허브는 절대 트랜스코딩하지 않는다(아키텍처 원칙, 목적/의도 섹션). youtube-adapter의 상시 재인코딩은 "무변환 원칙의 예외"가 아니라 이 원칙의 정규 적용 사례로 재프레이밍했다.
3. **`startedAt` → `lastUpdatedAt` 개명** — 코드가 반환하던 값은 시작 시각이 아니라 playlist mtime(폴링마다 전진)이며, web-backend가 디코드만 하고 미사용인 **죽은 필드**였다. 실 소비 신호는 `active` + `hlsUrl`뿐. 이름을 실제 의미(최종 갱신 시각)로 바로잡았다. 실제 시작 시각 추적은 소비자 요구가 생기기 전까지 채택하지 않는다(상태 컴포넌트·콜백 추가 = 현 단순성과 상충).
4. **상태 휘발성 균일화(tmpfs) + 소비자 부재==inactive** — `/tmp/hls`를 tmpfs로 마운트해 recreate·restart 모두 완전 초기화, 재기동 거짓 alive(SSOT 거짓 양성)를 제거한다(계약 4 "상태 휘발성", A4-6). dead 스트림 목록 잔존 수명은 불확정이나 소비자가 "부재"와 "`active:false`"를 동일 취급(MUST)하면 무해하다.
5. **CORS `Access-Control-Allow-Origin: *`는 의도된 것** — `/live/` 응답의 전면 개방은 프론트엔드 직접 재생 편의용이며 내부망(Docker network) 전용이라 실해가 없다. 외부 노출은 web-frontend nginx 프록시 경유 한정. 계약 2 응답 헤더로 유지한다.

### 하위 세션(어댑터/테스트 하니스)으로 이관된 사항 (허브 계약 범위 밖)

- **push 측 H.264 profile pin** — 어느 어댑터도 `-profile:v`를 명시하지 않아 profile은 preset 부작용으로 결정된다. 허브는 profile을 강제하지 않으므로(계약 1) 이는 허브 계약 문제가 아니다. profile 보장이 필요하면 각 어댑터 스펙에서 pin 여부를 결정한다.
- **키프레임 간격 ≤2초 보장** — 세그먼트 길이·초기 지연 최적화 문제이며(계약 위반 아님, 위반 시 push는 수락됨), 카메라/어댑터 설정 지침으로 각 어댑터 스펙에서 다룬다.
- **SSOT dead 전이의 CI 상시 검증 공백** — A4-3/A4-6이 mutating-gated라 상시 미검증인 것은 테스트 하니스 결함이며 계약 결함이 아니다. 별도 테스트 개선 트랙에서 처리.
