# Streaming API Specification — Sentinel

> **이 문서는 Sentinel 스트리밍 인터페이스의 단일 진실 원천(SSOT)입니다.**
> CCTV/카메라 어댑터 개발자(또는 외부 벤더, 다른 AI 세션)가 이 파일 하나만 보고 통합할 수 있도록 작성되었습니다.
> 스펙 변경 시 `services/streaming/` 코드와 본 문서를 같은 커밋에 수정해야 합니다.

---

## 1. 개요

`streaming` 서비스는 모든 영상 소스의 단일 수신 지점이자 클라이언트로의 단일 송출 지점입니다.

- **입력:** RTMP push (어댑터/카메라 → streaming)
- **출력:** HLS pull (브라우저 → web-backend가 URL 중계 → streaming)
- **변환:** **무 트랜스코딩 (remux only).** CPU 사용 최소화

## 2. RTMP Input Specification

신규 카메라/어댑터를 통합하려면 다음 규격을 정확히 준수해야 합니다.

| 항목 | 요구사항 |
|------|----------|
| Protocol | RTMP |
| Endpoint | `rtmp://streaming:1935/live/{streamKey}` (내부 네트워크) |
| Container | FLV (`-f flv`) |
| Video codec | H.264 — Baseline 또는 Main profile |
| Audio codec | AAC (LC, HE-AAC 등 모든 profile 허용) |
| **B-frames** | **금지** (nginx-rtmp v1.2.2가 ~5초 후 연결 끊음) |
| Encoding hint | 소스에 B-frame 포함 가능성 있으면 `-tune zerolatency` 또는 `-bf 0` 사용 |

### 알려진 함정

- **"Connection reset by peer" on audio packet muxing** — 실제 원인은 비디오 B-frame. 에러 메시지가 오인을 유도. B-frame 제거가 1순위 점검 포인트
- streamKey는 영숫자/하이픈 권장. 슬래시 금지

### FFmpeg 푸시 예시

```bash
ffmpeg -i <source> \
  -c:v libx264 -profile:v baseline -tune zerolatency -bf 0 \
  -c:a aac \
  -f flv rtmp://streaming:1935/live/cam-001
```

## 3. HLS Output Specification

streaming이 자동 생성·서빙. 브라우저는 web-backend가 알려준 상대 URL로 접근.

| 항목 | 값 |
|------|-----|
| Format | HLS (`m3u8` + `ts` 세그먼트) |
| URL pattern | `/live/{streamKey}/index.m3u8` (상대 경로) |
| Fragment duration | 2초 |
| Playlist length | 10초 (5개 세그먼트) |
| Cleanup | 자동 (오래된 .ts 자동 제거) |
| Latency | 일반적으로 5~10초 (HLS 특성) |

> **상대 URL 정책:** 클라이언트에 반환되는 모든 URL은 `/live/...` 형태의 상대 경로여야 합니다. Docker 내부 주소(`http://streaming:8080/...`) 노출 금지. nginx가 `/live/`를 streaming으로 프록시.

## 4. Stream Status — SSOT

`streaming` 서비스가 스트림 alive/dead 상태의 **유일한 권위**입니다. 다른 서비스(어댑터 포함)는 상태 보고하지 않습니다.

| 판정 | 기준 |
|------|------|
| `active: true` | 마지막 30초 이내에 playlist 파일이 갱신됨 |
| `active: false` | 그 외 |

## 5. HTTP API

| Endpoint | Method | 응답 |
|----------|--------|------|
| `/api/streams` | GET | 활성 스트림 목록: `[{cameraId, streamKey, hlsUrl, active}]` |
| `/healthz` | GET | 헬스체크 (200 OK) |

### `/api/streams` 응답 예시

```json
[
  {
    "cameraId": "cam-001",
    "streamKey": "cam-001",
    "hlsUrl": "/live/cam-001/index.m3u8",
    "active": true
  }
]
```

web-backend는 이 API만 호출하면 스트림 상태/URL을 알 수 있습니다.

## 6. 내부 아키텍처 (참고용)

단일 컨테이너에 두 프로세스:

| 프로세스 | 포트 | 역할 |
|----------|------|------|
| nginx-rtmp | 1935 (RTMP), 8080 (HTTP) | RTMP 수신 → `/tmp/hls/{streamKey}/`에 HLS 변환 |
| Go streaming-api | 8081 | `/healthz`, `/api/streams` 응답 |

nginx가 `/healthz`와 `/api/*`를 8080 → 8081로 프록시. 외부 통합 시 이 분리는 신경 쓸 필요 없음 (모두 8080으로 호출).

## 7. 어댑터 패턴

비-RTMP 소스(예: RTSP, ONVIF, 독자 프로토콜)는 **어댑터 컨테이너**를 만들어 RTMP로 변환 push.

- 참고 구현: `services/cctv-adapter/` (RTSP → RTMP)
- 어댑터는 streaming의 상태에 영향 없음. RTMP 푸시만 책임
- 새 어댑터 추가 시 `services/{name}-adapter/` 디렉토리 + docker-compose 진입점만 추가

## 8. 운영 정책

- **무 트랜스코딩 원칙 준수** — 어댑터에서 코덱 변환이 필요하면 가능한 한 copy 모드 사용
- **단일 호스트 / 경량** — mini PC 한 대에서 모든 스트림 동시 처리. 카메라 수 증가 시 CPU/IO 모니터링 필수
- **벤더 독립성** — 본 스펙만 준수하면 어떤 카메라/어댑터든 교체 자유. streaming/web-backend는 변경 없음

## 변경 이력 관리

이 문서가 SSOT입니다. 다음 코드 영역과 짝을 이룹니다:

- `services/streaming/` — nginx-rtmp 설정, Go API
- `services/cctv-adapter/main.go` — 참고 구현 (RTSP → RTMP)

스펙 변경 시 위 코드와 본 문서를 같은 커밋에 함께 수정하세요.
