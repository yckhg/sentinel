# Spec Index

> spec(계약) ↔ 검증 테스트 매핑. 테스트는 단언 ID별 스크립트로 `tests/spec/<spec-name>/`에 1:1 배치.
> mutating 테스트는 `ALLOW_MUTATING=1` (스트리밍군은 `SPEC_TDD_ALLOW_MUTATING=1`) 게이트 뒤에 격리 — 기본 SKIP.

## 인터페이스(접면) 스펙

| Spec | 계약 대상 | 테스트 위치 | 최근 판정 (2026-07-02) |
|---|---|---|---|
| [interface-mqtt.md](interface-mqtt.md) | MQTT 토픽 5종 (H/W ↔ Sentinel) | `tests/spec/interface-mqtt/` | OK 0 · NOK 0 · SKIP 21 (전건 mutating) |
| [interface-streaming.md](interface-streaming.md) | RTMP 입력 · HLS 출력 · RTMP 재배포 · 상태 SSOT | `tests/spec/interface-streaming/` | OK 8 · NOK 1 · SKIP 8 |
| [interface-web-api.md](interface-web-api.md) | REST/WS 엔드포인트 그룹 15종 | `tests/spec/interface-web-api/` | OK 11 · NOK 0 · SKIP 73 |

## 서비스 스펙

| Spec | 테스트 위치 | 최근 판정 (2026-07-02) |
|---|---|---|
| [hw-gateway.md](hw-gateway.md) | `tests/spec/hw-gateway/` | OK 1 · NOK 0 · SKIP 16 |
| [notifier.md](notifier.md) | `tests/spec/notifier/` | OK 1 · NOK 0 · SKIP 9 |
| [streaming.md](streaming.md) | `tests/spec/streaming/` | OK 4 · NOK 0 · SKIP 4 |
| [cctv-adapter.md](cctv-adapter.md) | `tests/spec/cctv-adapter/` | OK 2 · NOK 0 · SKIP 9 (카메라 0대 구성) |
| [youtube-adapter.md](youtube-adapter.md) | `tests/spec/youtube-adapter/` | OK 4 · NOK 0 · SKIP 5 |
| [recording.md](recording.md) | `tests/spec/recording/` | OK 13 · NOK 0 · SKIP 2 |
| [web-backend.md](web-backend.md) | `tests/spec/web-backend/` | OK 3 · NOK 0 · SKIP 16 |
| [web-frontend.md](web-frontend.md) | `tests/spec/web-frontend/` | OK 2 · NOK 0 · SKIP 12 (브라우저 필요분 Playwright 개요 주석) |

## 공용 헬퍼

- `tests/spec/lib-web.sh` — Docker 내 curl 래퍼 (web 계열)
- `tests/spec/lib/ws_client.py` — WebSocket 관찰 클라이언트 (구독 전용)
- `tests/spec/recording/common.sh`, `run_all.sh`

## 검증 리포트

- [VERIFICATION-REPORT.md](VERIFICATION-REPORT.md) — 초판 spec 간 정합성 검증 (2026-07-02, 접면 3 × 독립 검증자)

## SKIP 해제 조건 (설계자 승인 목록)

1. **mutating 승인**: `ALLOW_MUTATING=1` — 자체 fixture/cleanup 설계분은 즉시, 브로커 정지·컨테이너 재생성 계열은 입회/스테이징 권장
2. **admin fixture**: `ADMIN_TOKEN=` 또는 `ADMIN_PASSWORD=` 주입 시 인증 GET 단언 즉시 실행 가능
3. **cctv-adapter**: 테스트 RTSP 소스 도입 시 핵심 단언(C·D·I·J) 판정 가능
4. **web-frontend**: Playwright 세션 별도 실행
