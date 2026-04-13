# recording

> **Reader scope:** 이 서비스를 구현·수정하는 Claude 세션 전용.
> 다른 서비스의 내부 구현을 읽지 마세요. 외부와의 계약은 아래 "Interfaces" 섹션의 링크만 참조.
> 시스템 전체 그림은 orchestrator 세션 영역(`docs/architecture-overview.md`)이며 본 세션은 읽을 필요 없음.

## Responsibility

streaming 서버의 RTMP를 **구독**해 모든 카메라를 롤링 윈도우로 녹화하고(`ROLLING_WINDOW_MINUTES`분 유지), incident 발생 시 해당 구간 세그먼트를 보호(protect)·병합(finalize)하여 MP4 아카이브를 생성한다. HLS pseudo-playback으로 과거 구간을 브라우저에 돌려준다.

## Interfaces

| Boundary | Direction | Spec |
|----------|-----------|------|
| streaming 서버 (RTMP pull) | outbound (FFmpeg input) | [../interfaces/streaming-api.md](../interfaces/streaming-api.md) |
| web-backend (camera list fetch) | outbound | `GET /internal/cameras` |
| web-backend (proxy 원본) | inbound | 본 문서 "HTTP API" (web-backend의 `/api/recordings/*`, `/api/archives/*`, `/api/storage`가 이 서비스로 proxy됨) |

## Code Structure

단일 파일: `services/recording/main.go` (~1650 lines). 주요 컴포넌트:

- **RecordingManager** (`manageRecording`, main.go:112): 카메라별 FFmpeg 프로세스 관리. RTMP를 `-c copy -f segment`로 10초 단위 `.ts`로 저장. strftime 파일명 `%Y%m%d_%H%M%S.ts`.
- **Watchdog** (main.go:197): stdout/stderr 마지막 출력 시각 기반. `FFMPEG_TIMEOUT` 초과 시 SIGTERM → 5초 후 SIGKILL.
- **Rolling cleanup**: `ROLLING_WINDOW_MINUTES`(기본 60) 초과 + 미보호(unprotected) 세그먼트 자동 삭제.
- **ArchiveManager** (main.go:766 부근): 보호된 세그먼트들을 FFmpeg `-c copy`로 MP4 병합.
- **HTTP server** (main.go:1256~): `net/http` 라우팅.
- **Segment streaming**: `/api/recordings/{key}/play?from&to` → 해당 구간 `.ts`들을 나열한 동적 HLS playlist 생성.

## Environment Variables

| Var | Default | Meaning |
|-----|---------|---------|
| `STREAMING_RTMP_URL` | `rtmp://streaming:1935/live` | RTMP pull 베이스 |
| `WEB_BACKEND_URL` | `http://web-backend:8080` | camera list fetch |
| `RECORDINGS_DIR` | `/recordings` | 세그먼트 저장소 (volume `recordings-data`) |
| `ARCHIVES_DIR` | `/archives` | 영구 아카이브 MP4 (volume `archives-data`) |
| `ROLLING_WINDOW_MINUTES` | `60` | 보호되지 않은 세그먼트 보존 시간 |
| `FFMPEG_TIMEOUT` | `60` (초) | watchdog stall 임계치 |

## Build & Run

```bash
docker compose build recording
docker compose up -d recording
docker compose logs -f recording
```

- 포트: 내부 8080
- 헬스: `GET /healthz`
- 볼륨: `recordings-data` (롤링), `archives-data` (영구)
- 단독 확인: `docker exec sentinel-recording ls /recordings/{streamKey}/` 최신 `.ts` 생성 확인

## HTTP API

이 서비스의 `/api/*`는 web-backend가 `authMiddleware`로 감싼 뒤 그대로 proxy한다. 직접 호출 시 auth 없음(내부 망).

| Method | Path | Purpose |
|--------|------|---------|
| GET | `/healthz` | 헬스 |
| GET | `/api/status` | 카메라별 recorder 상태 (`recording`/`reconnecting`/`disconnected`) |
| POST | `/api/cameras/reload` | web-backend에서 최신 카메라 목록 재조회 후 recorder reconcile |
| GET | `/api/recordings/{stream_key}` | 사용 가능한 시간 범위 목록 |
| GET | `/api/recordings/{stream_key}/play?from=&to=` | 해당 구간 HLS playlist (동적 생성) |
| GET | `/api/recordings/{stream_key}/segments/{filename}` | `.ts` 세그먼트 파일 |
| POST | `/api/archives/protect` | **Phase 1**: incident 시간 범위 세그먼트를 rolling cleanup에서 보호 |
| POST | `/api/archives/finalize` | **Phase 2**: 보호 세그먼트를 MP4로 병합하여 영구 아카이브 생성 |
| POST | `/api/archives` | 임의 구간 즉시 아카이브 생성 |
| GET | `/api/archives` | 아카이브 목록 |
| GET | `/api/archives/{id}/download` | MP4 다운로드 |
| DELETE | `/api/archives/{id}` | 아카이브 삭제 |
| DELETE | `/api/archives/incident/{incidentId}` | incident의 모든 아카이브 삭제 |
| GET | `/api/storage` | 디스크 사용량 통계 |

## Outbound Calls

- **streaming** `rtmp://streaming:1935/live/{streamKey}` — FFmpeg input으로 지속 연결. 끊기면 exponential backoff 1s→30s 재연결.
- **web-backend** `GET /internal/cameras` — reload 시 카메라 목록 조회.

## Constraints / Known Issues

- **두 단계 아카이브(protect → finalize)** 패턴: crisis 직후 protect로 삭제 방지 → 상황 종료 후 finalize로 MP4 병합. 이 순서를 지키지 않으면 rolling cleanup에 의해 원본 `.ts`가 소실될 수 있음.
- 세그먼트 파일명은 `%Y%m%d_%H%M%S.ts` UTC 기준. 시간 범위 쿼리는 ISO8601로 받지만 내부 비교는 파일명 파싱.
- FFmpeg watchdog은 조용히 멈춘 프로세스를 복구하는 핵심. 로그에서 `FFmpeg output timeout` 메시지 확인.
- 0바이트 `.ts` 세그먼트(네트워크 단절 등)는 별도 정리 로직 존재 (US-003).
- segment는 `-c copy`이므로 streaming의 B-frame 제약이 그대로 적용 — 이미 streaming이 받은 스트림이면 문제 없음.

## Storage / State

- `/recordings/{streamKey}/*.ts` (rolling, volume `recordings-data`)
- `/archives/{archiveId}.mp4` (영구, volume `archives-data`)
- In-memory: recorder 상태 map, ArchiveManager 진행 상태, 보호 세그먼트 집합.
- DB 없음. 아카이브 메타는 파일 시스템 + in-memory (재시작 시 재스캔, 구현 확인).
