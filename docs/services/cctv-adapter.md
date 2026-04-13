# cctv-adapter

> **Reader scope:** 이 서비스를 구현·수정하는 Claude 세션 전용.
> 다른 서비스의 내부 구현을 읽지 마세요. 외부와의 계약은 아래 "Interfaces" 섹션의 링크만 참조.
> 시스템 전체 그림은 orchestrator 세션 영역(`docs/architecture-overview.md`)이며 본 세션은 읽을 필요 없음.

## Responsibility

RTSP CCTV 카메라를 streaming 서버의 RTMP 입력 규격에 맞춰 push하는 adapter. 어댑터 패턴의 **참조 구현**이며, 새 소스 타입의 adapter는 이 구조를 따른다.

## Interfaces

| Boundary | Direction | Spec |
|----------|-----------|------|
| RTSP 카메라 | inbound (pull) | 카메라 장비 고유 RTSP URL |
| streaming 서버 (RTMP push) | outbound | [../interfaces/streaming-api.md](../interfaces/streaming-api.md) — "RTMP Input Specification" |
| web-backend (camera list fetch + reload trigger) | both | 본 문서 "HTTP API" / "Outbound Calls" |

## Code Structure

- 단일 파일: `services/cctv-adapter/main.go`
- 카메라별 goroutine이 독립 FFmpeg 프로세스를 관리 (connect → monitor → reconnect)
- Hot reload: `POST /api/cameras/reload` 수신 시 web-backend에서 최신 카메라 목록을 fetch → 기존 FFmpeg와 diff하여 추가/삭제
- FFmpeg watchdog: 출력 stall 감지 시 SIGTERM → SIGKILL

## Environment Variables

| Var | Default | Meaning |
|-----|---------|---------|
| `CAMERAS_CONFIG_PATH` | `/config/cameras.json` | 초기 부트용 카메라 목록 (compose read-only mount) |
| `STREAMING_RTMP_URL` | `rtmp://streaming:1935/live` | RTMP push 베이스 URL |
| `WEB_BACKEND_URL` | `http://web-backend:8080` | reload 시 카메라 목록 fetch |
| `FFMPEG_TIMEOUT` | `30` (초) | 출력 stall 감지 임계치 |

`cameras.json` 샘플: `services/cctv-adapter/cameras.example.json`.

## Build & Run

```bash
docker compose build cctv-adapter
docker compose up -d cctv-adapter
docker compose logs -f cctv-adapter
```

- 포트: 내부 8080 (헬스만)
- 헬스: `GET /healthz`
- 단독 테스트: `cameras.json`에 한 대 설정 후 `curl rtmp://streaming:1935/live/{key}` 재생 또는 streaming의 HLS endpoint로 확인

## HTTP API

| Method | Path | Purpose |
|--------|------|---------|
| GET | `/healthz` | 헬스 |
| POST | `/api/cameras/reload` | web-backend에서 최신 목록을 다시 가져와 FFmpeg 프로세스 reconcile |

## Outbound Calls

- **RTMP push** → `rtmp://streaming:1935/live/{streamKey}` (H.264 no B-frame + AAC, FLV). 스펙은 [../interfaces/streaming-api.md](../interfaces/streaming-api.md) 참조.
- **web-backend** `GET {WEB_BACKEND_URL}/internal/cameras` — reload 시 카메라 목록 조회 (응답: `[{id, streamKey, rtspUrl, enabled}]`)

## FFmpeg Invocation

기본은 `-c copy -f flv` (대부분의 산업용 CCTV는 baseline H.264, no B-frame).
카메라가 B-frame을 출력하면 다음으로 전환 필요:
```
-c:v libx264 -tune zerolatency -bf 0 -c:a aac -f flv
```

## Constraints / Known Issues

- 카메라당 FFmpeg 1프로세스. 메모리 256M 제한(compose) 내에서 동시 수용 가능 수량 주의.
- 재연결: exponential backoff 1s → 30s, clean exit 시 backoff reset.
- Hot reload는 목록 diff 기반 — 기존 카메라의 RTSP URL이 바뀌면 명시적으로 제거 후 추가 필요할 수 있음 (구현 확인 필요).
- watchdog는 stdout/stderr 마지막 출력 시각 기반 → 조용히 block되는 FFmpeg는 정상 탐지.

## Storage / State

- 영구 저장 없음. 카메라 config는 파일+API fetch, 런타임 상태는 in-memory(FFmpeg cmd 핸들 + 상태 map).
