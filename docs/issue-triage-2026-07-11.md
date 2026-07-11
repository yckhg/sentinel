# 이슈 트리아지 — #39~#105 (2026-07-11)

전수 발굴로 등록된 67건(#39~#105)을 **스펙-우선 방법론** 기준으로 3분류.

- **①SPEC (스펙 수정 필요)** — 계약/의도가 `docs/spec/`·`docs/interfaces/`에 없거나·모호하거나·정책 결정이 선행돼야 함. → `spec-write`로 스펙 먼저 확정 후 구현.
- **②FIX (단순 수정)** — 스펙은 그대로, 코드/설정만 고치면 됨. 자명한 버그·경합·파싱·설정 누락. → spec-tdd 게이트 → 독립구현 파이프라인 바로 투입 가능.
- **③OTHER (기타)** — 인프라·docker-compose·빌드·순수 테스트·문서 동기화·설계 논의.

| 분류 | 건수 |
|------|------|
| ①SPEC | 19 |
| ②FIX | 41 |
| ③OTHER | 7 |
| **합계** | **67** |

---

## ① 스펙 수정 필요 — 19건

스펙 파일별 그룹핑 (구현 전 Designer 결정 필요):

| 스펙 파일 | 이슈 | 핵심 |
|-----------|------|------|
| **notifier.md** | #57 #59 #60 #61 #63 #64 #65 (7) | 무인증 신뢰경계·전달보장(outbox)·dedup/throttle·재시도정책·보호요청 순차/병렬·site 필터 — 전부 계약 결정 선행 |
| **hw-gateway.md** | #51 #52 #53 (3) | healthz 200 계약(브로커 죽어도 200)·clean session 유실허용·equipment 보존/eviction 정책 재결정 |
| **web-backend.md** | #48 #83 (2) | autoheal 정책·unhealthy 전이 push 인터페이스 / 비번변경 시 JWT 무효화(현 스펙은 만료 전 유효를 보장) |
| **interface-web-api.md** | #82 #95 (2) | 수립된 WS의 토큰 폐기 재검증 계약 / 헬스 임계값 벌크저장 엔드포인트 신설(부분저장 원자성) |
| **web-frontend.md** | #92 #104 (2) | WS 끊김 사용자 표시+재동기화 UX 미정의 / 상태기반 라우팅(line75 정본)→딥링크·404·미인증 리다이렉트는 계약 변경 |
| **web-api.md (interfaces)** | #49 (1) | SSOT 예시가 절대 내부URL — 상대URL 정책과 모순(스펙이 틀림) |
| **youtube-adapter.md** | #72 (1) | 인코딩 파라미터 env 노출 = 새 설정 인터페이스(§25 env 목록 확장), 기본값 계약 선결 |
| **recording.md** | #75 (1) | 재시작 시 processing/finalizing 아카이브 복구 정책(§112 재시도/실패 처리 플래그) |

**착수 팁:** notifier 7건이 한 파일에 몰려 있어, 신뢰경계·전달보장·과금정책 방향을 한 번에 정하는 것이 효율적.

---

## ② 단순 수정 — 41건 (스펙 불변, 바로 구현)

| 영역 | 이슈 |
|------|------|
| 크로스/인프라 | #39 #40 #41 #43 #44 #46 |
| hw-gateway/notifier | #55 #58 #62 |
| cctv/youtube/recording | #66 #67 #69 #70 #71 #73 #74 #76 #77 #78 #79 #80 #81 |
| web-backend | #84 #85 #86 #87 #88 #89 |
| web-frontend | #90 #91 #93 #94 #96 #97 #98 #99 #100 #101 #102 #103 #105 |

주요 근거:
- #39 http.Server 타임아웃 / #40 graceful shutdown / #41 MaxBytesReader / #43 PII 마스킹(#30 선례) / #44 Reload 병렬 grace / #46 compose env 주입 누락
- #55 #62 outbound/fan-out 세마포어 상한 / #58 goroutine recover()
- #66 state.cmd 레이스+orphan / #67 tight-loop backoff / #69 stopCh 취소 / #70 이중 close panic / #71 localFile /media/ 강제(doc 계약 이미 존재) / #73 경로순회 검증 / #74 metadata temp+rename / #76 병합 후 원본 unprotect / #77 TZ=UTC 강제 / #78 EXTINF 실측+DISCONTINUITY / #79 Protect TOCTOU 락 / #80 0바이트 활성 세그먼트 유예 / #81 /api/storage 캐시
- #84 헬스 프로브 병렬 / #85 strconv 파싱 / #86 datetime() 비교 / #87 레이트리미터 원자화 / #88 오도 주석·죽은코드 제거 / #89 parseJWT alg 고정·trim 통일
- #90 base64url 디코딩 / #91 클릭 div 접근성 / #93 언마운트 cleanup / #94 모달 ESC·포커스트랩 / #96 HLS 자동복구 / #97 배너 dedupe+key / #98 GPS 주소 병기 / #99 ErrorBoundary / #100 ARIA 보강 / #101 타임라인 라이브추종 / #102 달력 바깥클릭 / #103 에러표시 일관화 / #105 WCAG 대비·터치타겟

---

## ③ 기타 — 7건

| 이슈 | 성격 |
|------|------|
| #45 | sentinel-ffmpeg-base compose 빌드 누락 (인프라) |
| #47 | Docker 로그 로테이션 미설정 (compose 인프라) |
| #50 | mosquitto 메모리 제한 (compose 인프라) |
| #56 | hw-gateway Dockerfile 하드닝 + 로그 truncate 소품 |
| #42 | Go 단위 테스트 전무 (테스트 추가 전용) |
| #54 | 레거시 `docs/services/hw-gateway.md` 동기화 (스펙 §187은 이미 확정) |
| #68 | watchdog liveness 오탐 — 하드웨어 접합(#28)·설계 논의 필요(question) |

---

## 권장 작업 순서

1. **①SPEC 19건** — `spec-orchestrate`로 바로 못 돌림. Designer가 이슈별로 "무엇이 옳은가"를 스펙에 먼저 기술. notifier 7건 정책 결정을 우선.
2. **②FIX 41건** — 완료된 3-leaf 방식(spec-tdd 게이트 → 독립구현 → 독립검증) 그대로 병렬 투입 가능.
3. **③OTHER 7건** — compose/인프라 소품(#45 #47 #50 #56)은 묶어서 한 번에. #42 테스트·#54 문서·#68 논의는 별도.

> 방법론: `~/.claude/docs/roles/` (designer/orchestrator/implementer/verifier), 툴킷 spec-kit.
> 트리아지 방식: 영역별 read-only 서브에이전트 5종 병렬 분류(이슈 본문 + 관련 스펙 대조).
