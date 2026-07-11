# Spec Index

> spec(계약) ↔ 검증 테스트 매핑. 테스트는 단언 ID별 스크립트로 `tests/spec/<spec-name>/`에 1:1 배치.
> mutating 테스트는 `ALLOW_MUTATING=1` (스트리밍군은 `SPEC_TDD_ALLOW_MUTATING=1`) 게이트 뒤에 격리 — 기본 SKIP.

> ⚠️ **최근 판정(2026-07-10)의 런타임 부분은 스테일 컨테이너 기준.** 실행 이미지(web-backend 2026-04-27 · notifier 2026-04-22)가 오늘자 fix(#19~#25, A-1~A-5)를 미반영 → 재배포 후 재검증 필요. 상세 [VERIFICATION-REPORT-0710.md](VERIFICATION-REPORT-0710.md) 헤드라인 참조.

## 인터페이스(접면) 스펙

| Spec | 계약 대상 | 테스트 위치 | 최근 판정 (2026-07-10) |
|---|---|---|---|
| [interface-mqtt.md](interface-mqtt.md) | MQTT 토픽 5종 (H/W ↔ Sentinel) | `tests/spec/interface-mqtt/` | OK 0 · NOK 0 · SKIP 22 (전건 mutating; 단언 22 vs 테스트 21 — RS-6 무테스트) |
| [interface-streaming.md](interface-streaming.md) | RTMP 입력 · HLS 출력 · RTMP 재배포 · 상태 SSOT | `tests/spec/interface-streaming/` | OK 9 · NOK 0 · SKIP 8 (healthz 게이트 false-NOK 버그) |
| [interface-web-api.md](interface-web-api.md) | REST/WS 엔드포인트 그룹 15종 | `tests/spec/interface-web-api/` | OK 11 · NOK 0 · SKIP 73 (admin fixture 부재) |

## 서비스 스펙

| Spec | 테스트 위치 | 최근 판정 (2026-07-10) |
|---|---|---|
| [hw-gateway.md](hw-gateway.md) | `tests/spec/hw-gateway/` | **OK 22코어+f2/e2 · NOK 0 · SKIP 1 (a3 per-topic SUBACK=load-bearing, 유닛 대체커버)** (2026-07-11 spec/followup-2 재검증, Go unit 9/9) |
| [notifier.md](notifier.md) | `tests/spec/notifier/` | OK 8 · NOK 0 · SKIP 2 (최후 보루 79/79 401 = 드리프트) |
| [streaming.md](streaming.md) | `tests/spec/streaming/` | OK 3 · NOK 0 · SKIP 5 (healthz 게이트 false-NOK 버그) |
| [cctv-adapter.md](cctv-adapter.md) | `tests/spec/cctv-adapter/` | OK 2 · NOK 0 · SKIP 9 (카메라 0대 구성) |
| [youtube-adapter.md](youtube-adapter.md) | `tests/spec/youtube-adapter/` | **OK J·J-2·C·B·F부분 · NOK 0 · SKIP G(reload mock 부재)·F완전** (2026-07-11 재검증, #72 인코딩 env, encode_test.go PASS) |
| [recording.md](recording.md) | `tests/spec/recording/` | **OK P·P-2·O·F·G·M 외 · NOK 0 · SKIP D(⚠#1 삭제시 보호미해제=pre-existing 설계판단)** (2026-07-11 재검증, #75 기동복구, recovery_test.go 4/4 PASS) |
| [web-backend.md](web-backend.md) | `tests/spec/web-backend/` | OK 3 · NOK 0 · SKIP 16 (admin fixture 부재; 보안 CRITICAL 2) |
| [web-frontend.md](web-frontend.md) | `tests/spec/web-frontend/` | OK 2 · NOK 0 · SKIP 12 (needs-browser; Playwright 별도 세션) |

## 공용 헬퍼

- `tests/spec/lib-web.sh` — Docker 내 curl 래퍼 (web 계열)
- `tests/spec/lib/ws_client.py` — WebSocket 관찰 클라이언트 (구독 전용)
- `tests/spec/recording/common.sh`, `run_all.sh`

## 검증 리포트

- [VERIFICATION-REPORT-0710.md](VERIFICATION-REPORT-0710.md) — **최신** 준수 감사 (2026-07-10, 스펙 11종 × 독립 검증자 11명, 비파괴·코드-실측 타이브레이크·강제-5). 집계 OK 56 · NOK 0 · SKIP 170(≈75% 공허). **헤드라인: 런타임 판정은 스테일 컨테이너(4월 빌드) 측정 — 오늘자 fix 미배포 드리프트, 재배포 후 재검증 필요.** 핵심 SKIPPED ≈136건, CRITICAL 3 · HIGH ~20.
- [VERIFICATION-REPORT.md](VERIFICATION-REPORT.md) — 초판 spec 간 정합성 검증 (2026-07-02, 접면 3 × 독립 검증자)

## SKIP 해제 조건 (설계자 승인 목록)

0. **[선결] 스테일 컨테이너 재배포**: 실행 이미지(web-backend 2026-04-27 · notifier 2026-04-22)가 오늘자 fix(#19~#25) 미반영 → 재배포로 활성화해야 런타임 판정이 유효. 다른 모든 해제의 전제.
1. **mutating 승인**: `ALLOW_MUTATING=1`(스트리밍군 `SPEC_TDD_ALLOW_MUTATING=1`) + **격리 스택(별도 DB 볼륨/더미 RTMP/mock 장비)** — 자체 fixture/cleanup 설계분은 즉시, 브로커 정지·컨테이너 재생성·물리 디바이스(RS-5) 계열은 입회/스테이징 권장
2. **admin fixture**: `ADMIN_TOKEN=` 또는 `ADMIN_PASSWORD=` 주입 시 인증 GET 단언 즉시 실행(interface-web-api 11건 + web-backend 인증 GET 다수). `USER_TOKEN=` 주입 시 user-role 403 단언 9건 추가 해제
3. **cctv-adapter**: 테스트 RTSP 소스(카메라 소스) 도입 시 push/스트리밍 핵심 단언(C·D·F·G·H·I·J·K) 판정 가능
4. **web-frontend**: Playwright 세션 별도 실행(needs-browser 핵심 7건: B·C·D·H·I·J·K)
5. **테스트 인프라 부채**: RS-6 retained-resolve 테스트 신규 작성, streaming/interface-streaming healthz 게이트 파싱 버그 수정, recording 스테이징 recorder(더미 RTMP + 격리 볼륨)
