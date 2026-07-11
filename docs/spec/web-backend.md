# web-backend 스펙
> 상태: 살아있는 계약 (living spec) · 독자: 스펙 작성자

## 목적 / 의도

web-backend는 Sentinel 시스템의 **중앙 상태 소유자**다. 모든 영속 데이터(사용자, 연락처, 현장, 카메라, 사고, 장비, 초대, 설정, 건강 이력)는 이 서비스가 소유한 단일 SQLite 파일에만 존재한다. 이 서비스는 다음을 보장한다.

1. **단일 인증 관문** — 웹/모바일 클라이언트의 모든 보호 자원 접근은 이 서비스가 발급한 JWT로만 가능하다.
2. **사고(incident) 생명주기의 단일 심판** — 사고의 생성·확인(acknowledge)·해소(resolve) 상태 전이는 오직 이 서비스가 판정하고 기록하며, 웹 사용자와 현장 센서 어느 쪽이 해소했는지 귀속(attribution)을 남긴다.
3. **실시간 전파** — 사고 발생/해소는 접속 중인 모든 WebSocket 클라이언트에게 즉시 push된다.
4. **서비스 간 조정자** — 녹화/아카이브 조회는 recording 서비스로 proxy하고, 장비 재시작은 hw-gateway로 전달하며, 카메라 구성 변경 시 adapter들에게 reload를 트리거한다.
5. **시스템 건강의 관측자** — 동료 서비스와 현장 센서의 생존 여부를 주기 감시하고, 상태 전이 이력을 영속한다.

## 언어 · 런타임

- Go (표준 `net/http` 라우팅, 외부 프레임워크 없음). 단일 정적 바이너리로 Docker 컨테이너에서 구동.
- 컨테이너 내부 `:8080` 리스닝. 외부 노출은 상위 proxy의 책임.

## 의존 도구 · 시스템

- **SQLite** (`modernc.org/sqlite`, CGO-free) — `/data/sentinel.db` 단일 파일. WAL 모드, busy_timeout 5s, foreign_keys ON.
- **JWT** (HS256, `golang-jwt/v5`) — 로그인 토큰과 임시 열람 토큰 모두 동일 secret으로 서명.
- **bcrypt** — 비밀번호 해시 저장. 평문은 어디에도 저장되지 않는다.
- **gorilla/websocket** — `/ws` push 채널.
- **동료 서비스 (HTTP, Docker 내부 네트워크)**: hw-gateway(재시작·해소 전파·테스트 알림), streaming(활성 스트림 목록), cctv-adapter/youtube-adapter(카메라 reload 트리거), recording(녹화/아카이브/스토리지 proxy 대상, 아카이브 finalize), notifier(초대 이메일 발송).
- 동료 서비스 down은 이 서비스의 기동·핵심 기능을 막지 않는다(호출은 best-effort 또는 502 반환).

## 입력

| 입력 | 출처 | 성격 |
|------|------|------|
| REST 요청 (`/auth/*`, `/api/*`, `/internal/*`) | web-frontend, 내부 서비스 | 스키마 SSOT는 `docs/spec/interface-web-api.md` |
| `POST /api/incidents` | hw-gateway | 위기 발생 보고 (무인증, 네트워크 격리 전제) |
| `POST /api/devices/seen` | hw-gateway | 장비 heartbeat/alert/candidate 관측 보고 (무인증) |
| `POST /api/incidents/{id}/resolve-from-sensor` | hw-gateway | 센서 버튼에 의한 해소 보고 (무인증) |
| WebSocket 접속 (`/ws?token=...`) | 클라이언트 | 일반 JWT 또는 임시 링크 JWT |
| 환경변수 | compose | `DB_PATH`, `JWT_SECRET`, `ADMIN_USERNAME`/`ADMIN_PASSWORD`, `HW_GATEWAY_URL`, `STREAMING_URL`, `CCTV_ADAPTER_URL`, `YOUTUBE_ADAPTER_URL`, `RECORDING_URL`, `NOTIFIER_URL`, `FRONTEND_URL` — 전부 미설정 시 Docker 서비스명 기반 기본값으로 동작 |
| system_settings 테이블 | 자체 DB | 건강 감시 주기/임계값(`health.*`), 외부 사이트 URL(`site_url`)을 런타임에 재읽기 — 재시작 없이 수 초 내 반영 |

## 출력 (계약)

- **HTTP/WebSocket 인터페이스 전체** — 엔드포인트 경로·요청/응답 스키마·에러 코드 매핑의 소유자는 `docs/spec/interface-web-api.md`이다. 본 스펙은 그 스키마를 재정의하지 않고, 그 위의 보장만 기술한다.
- **WebSocket push** — `crisis_alert`(사고 생성 시, 전 클라이언트), `incident_resolved`(해소 시, 전 클라이언트), `connected`(접속 직후 본인에게). 메시지는 `{type, payload, timestamp(RFC3339 UTC)}` 봉투를 가진다. push 전용이며 클라이언트가 보낸 메시지는 처리하지 않는다. 느린 클라이언트로의 push는 차단 없이 유실될 수 있으며, 한 클라이언트의 지연이 다른 클라이언트로의 전파를 막지 않는다.
- **SQLite 영속 상태** — 스키마의 SSOT는 코드 내 마이그레이션 목록이며, 버전 번호 기반으로 멱등 적용된다 — 같은 버전은 두 번 적용되지 않고(적용 이력은 DB에 영속), 각 마이그레이션은 트랜잭션 단위로 전부-또는-전무다.
- **동시 쓰기 내구성** — WAL 모드 + `busy_timeout`(5s)로 다수 동시 쓰기 요청을 직렬화해 처리하며, 정상 부하에서 `SQLITE_BUSY`로 인한 5xx나 쓰기 유실을 발생시키지 않는다(동시 도착 쓰기는 대기 후 성공하거나 명시적 계약 상태코드로 응답할 뿐, 락 경합이 사용자에게 5xx로 새지 않는다).
- **WAL 위생** — `-wal` 파일은 주기적 체크포인트로 경계가 유지되어, 지속 쓰기 부하 하에서도 무한 성장하지 않고 고정 상한 내에 머문다. 부하가 멎으면 체크포인트가 WAL 프레임을 회수(truncate)해 파일이 축소된다. 커넥션 풀이 다수 유휴 reader를 상시 보유하더라도 이 경계는 유지된다.
- **outbound 호출** — 아카이브 finalize(해소 시), hw-gateway 해소 전파(웹 해소 시), adapter reload(카메라 CUD 시), 초대 이메일(초대 생성 시)은 모두 **비동기 best-effort**다. 실패해도 원 요청의 성공 응답은 바뀌지 않으며 로그만 남는다.
- **설정 원자적 저장** — 여러 건강 임계값/설정을 한 번에 변경하는 벌크 저장은 **전부-또는-전무**다(단일 트랜잭션). 요청 내 하나라도 미지의 key거나 값 검증에 실패하면 어떤 key도 커밋되지 않아, 순차 개별 저장이 중간에 실패해 일부만 반영되는 부분 저장 상태가 발생하지 않는다. 벌크 엔드포인트 스키마의 계약 소유자는 `docs/spec/interface-web-api.md`(계약 11)이다.

## 핵심 로직 (동작)

### 인증·인가
- 사용자 상태 기계: `pending → active`(admin 승인 또는 유효한 초대 토큰으로 가입 시 즉시) / `pending → rejected`. `active`가 아닌 계정은 올바른 자격증명으로도 로그인할 수 없다(403).
- 로그인 JWT는 24시간 유효, `userId`와 `role`(`admin`/`user`)을 담는다. `/api/*` 전체는 인증 미들웨어 뒤에 있으며, admin 전용 자원(연락처 CUD, 카메라 CUD, 현장, 초대, 설정, 링크 관리, 사고 ack/resolve, 삭제 장비 목록, 테스트 알림)은 role까지 검사한다.
- **비밀번호 변경 시 기존 발급 토큰 무효화**: 사용자가 비밀번호를 변경하면, 그 사용자에게 **변경 시점 이전에 발급된 모든 로그인 JWT는 즉시 무효**가 되어 `/api/*`·직접 검증 경로(`/auth/pending|approve|reject|users`)·`/ws`에서 `401`로 거부된다. 이는 관측 가능한 **자격증명 변경 경계**로 성립한다 — 각 사용자에 대해 서버가 유지하는 경계보다 이르게 발급된 토큰은 거부되고, 경계 이후 재로그인으로 발급된 토큰만 유효하다(구현 방식은 계약이 아님). 변경에 사용한 토큰 자신도 이후 무효가 되어 클라이언트는 재로그인해야 한다. 탈취 토큰이 비밀번호 변경 후에도 만료(24h)까지 생존하지 못한다. **비밀번호를 변경하지 않은 사용자의 토큰은 만료 전까지 계속 유효하다(단언 Q).**
- `/auth/pending`·`/auth/approve`·`/auth/reject`·`/auth/users`는 `/api/` 밖이지만 핸들러가 직접 admin JWT를 검증한다.
- 최초 기동 시(해당 username 부재 시) admin 계정을 자동 시드한다. 기본 자격은 `admin`/`sentinel1234`이며 기본값 사용 시 경고 로그를 남긴다.
- `JWT_SECRET` 미설정 시 secret을 자동 생성해 데이터 볼륨에 파일로 영속한다 — 재시작해도 발급된 토큰은 만료 전까지 유효하다.
- 임시 열람 링크: DB 저장 없는 JWT(24시간). 발급 목록과 폐기(blacklist)는 in-memory이므로 **컨테이너 재시작 시 목록·폐기 이력이 소실되고, 이미 발급된 토큰은 만료까지 다시 유효해진다**(선언된 한계). 만료 링크는 1시간 주기로 메모리에서 청소된다.
- rate limit은 login(10/분/IP)·register(5/분/IP)에만 적용되고 초과 시 429를 반환한다.

### 사고 생명주기
- 상태 기계: `open → acknowledged → resolved`, 그리고 `open → resolved`. **resolved는 종착 상태다** — resolved에 대한 acknowledge/재resolve는 409로 거부된다.
- 생성(무인증 internal 경로): 요청에 `alertId`가 있으면 동일 `alertId`의 기존 사고를 반환하고 **중복 행을 만들지 않는다**(멱등, DB unique 인덱스로도 보강). 이 멱등은 **동시성 하에서도 원자적으로 성립한다** — 같은 `alertId`로 N건이 동시에 도착해도 정확히 1건만 생성되고 나머지는 기존 사고를 에러 없이 `200`으로 반환하며, UNIQUE 부분 인덱스 충돌로 인한 5xx나 중복 행·유실이 발생하지 않는다(dedup 조회→삽입이 경쟁 창 없이 처리된다). 단, **현재 유일한 호출자인 hw-gateway는 `alertId`를 전송하지 않는다** — 이 DB 멱등 경로는 API 계약으로는 존재하나 실경로에서는 발화하지 않으며, 실운영의 중복 제거는 hw-gateway의 in-memory dedup이 담당한다. `deviceId`가 오면 devices 테이블에 upsert되며 soft-delete 상태였다면 자동 복구된다.
- 웹 해소: admin만 가능, 해소 노트 필수. 귀속은 `resolved_by_kind='web'` + 사용자 식별/표시명. 해소 성공 시 (a) recording에 아카이브 finalize 요청, (b) hw-gateway에 해소 사실 전파(센서 LED 등 동기화), (c) WS `incident_resolved` broadcast — 모두 비동기.
- 센서 해소(hw-gateway 발): 사고 id를 특정하지 않으면(0) 해당 site의 **가장 최근 미해결 사고**를 해소한다. 귀속 kind 기본값은 `sensor_button`. hw-gateway로의 역전파는 하지 않는다(루프 방지). finalize·WS broadcast는 웹 해소와 동일.
- 목록 조회는 페이지네이션(기본 20, 최대 100)과 기간/상태 필터를 제공하며 발생시각 내림차순이다.

### 장비(devices) 영속
- 장비는 클라이언트가 등록하는 것이 아니라 **hw-gateway의 관측 보고로 자동 영속**된다(`(site_id, device_id)` 유일). 보고가 올 때마다 `last_seen`이 갱신되고 soft-delete가 자동 해제된다.
- 삭제는 soft-delete(`deleted_at`)이며 복원 가능하다. 일반 목록은 미삭제만, `/all`(admin)은 삭제 포함 전체를 반환한다.
- 장비 재시작 요청은 **devices에 등록되어 있고 미삭제인 장비만** hw-gateway로 전달된다. 미등록/삭제 장비는 400. hw-gateway의 응답 상태·본문이 그대로 반환된다.

### 카메라
- 생성 시 `cam-{8 hex}` 형식의 stream_key가 서버에서 유일하게 자동 부여되며 **이후 절대 변경되지 않는다**(불변).
- `sourceType`은 `rtsp`|`youtube`만 허용. source URL은 스킴 검증(rtsp는 `rtsp(s)://`, youtube는 정규 유튜브 URL) + SSRF 방어(loopback/사설/링크로컬 raw IP 거부)를 통과해야 한다.
- 카메라 생성/수정/삭제 성공 시 cctv-adapter와 youtube-adapter 양쪽에 reload를 비동기 트리거한다.
- 목록 응답의 `hlsUrl`/`status`(connected/disconnected)는 streaming 서비스의 활성 스트림 목록과 조인해 채워지며 10초 캐시된다. streaming이 죽어 있으면 전부 disconnected로 표시될 뿐 목록 자체는 성공한다.

### 건강 감시 (HealthMonitor)
- 6개 동료 서비스의 `/healthz`를 주기 폴링(기본 30초, `system_settings`로 조정, 하한 5초)한다. **실패가 임계(기본 90초) 이상 지속되어야** unhealthy로 전이하고(플래핑 방지), 성공은 즉시 healthy로 복귀시킨다.
- 미삭제 장비의 `last_seen` 나이가 임계(기본 60초)를 넘으면 해당 센서를 unhealthy로 판정한다. soft-delete된 장비는 감시 대상에서 즉시 제외된다.
- **상태가 전이될 때만** `health_events`에 1행을 기록한다(전이당 정확히 1건). 현재 상태 스냅샷은 in-memory(재시작 시 휘발, 이력은 DB에 영속)이며, 조회 시 unhealthy 우선으로 정렬된다.
- **unhealthy 전이의 표면화(관측·능동 통지)**: 어떤 감시 대상(동료 서비스/센서)이 `healthy → unhealthy`로 전이하면, 이 전이는 `health_events` 기록에 그치지 않고 **접속 중 admin WS 클라이언트에 `system_alarm`(interface-web-api.md 계약 14)으로 능동 push**되어 표면화된다. `unhealthy → healthy` 복귀도 동일하게 admin에게 표면화된다. 이 통지 실패는 감시 루프를 막지 않는다(best-effort). (산업안전 관제 인프라 자체의 무응답이 조용히 방치되지 않아야 한다는 불변식.)
- **감시 대응 범위 (설계자 결정):** 본 스펙은 **"unhealthy 전이의 admin WS push"까지만** 계약한다. hang 상태(프로세스 생존·healthcheck 실패) 서비스의 자동 컨테이너 재시작은 인프라(docker compose restart policy) 경계로 두어 앱 책임에서 제외하며, unhealthy 전이의 notifier 외부 채널 팬아웃(SMS 등)도 채택하지 않는다.

### proxy 동작
- 녹화/아카이브/스토리지 요청은 경로·쿼리를 보존한 채 recording 서비스로 그대로 전달하고, 응답 상태·헤더·본문을 그대로 되돌린다. recording 도달 실패 시 502.
- 테스트 알림은 admin 전용이며 hw-gateway로 고정 페이로드(`siteId=test`)를 전달한다.

## 검증 단언 (TDD)

이하 `$B`는 web-backend 베이스 URL, `$DB`는 `/data/sentinel.db`, `$T`는 admin 로그인 토큰.

- A. **무인증 헬스체크**: `curl -s $B/healthz` → HTTP 200, body `{"status":"ok","service":"web-backend"}`.
- B. **인증 관문**: `curl -s -o /dev/null -w '%{http_code}' $B/api/cameras` (헤더 없음) → `401`. 같은 요청에 `Authorization: Bearer $T` → `200`.
- C. **로그인 계약**: 올바른 자격 `POST $B/auth/login` → 200 + `token`·`user.role`. 틀린 비밀번호 → 401. `pending` 계정 → 403.
- D. **가입 승인 상태 기계**: 초대 없이 register → 201에 `status:"pending"`, 즉시 로그인 시도 → 403. admin이 `POST /auth/approve/{id}` → 이후 로그인 200. SQLite 검증: `SELECT status FROM users WHERE username='<u>'` 가 `pending → active`로 변한다.
- E. **초대 자동 승인**: 유효 inviteToken으로 register → 201에 `status:"active"`, `SELECT status FROM invitations WHERE token='<t>'` → `accepted`. 만료/취소 토큰이면 가입은 되지만 `pending`이다.
- F. **rate limit**: 동일 IP에서 `POST /auth/login`을 1분 내 11회 → 11번째 응답 `429`.
- G. **사고 생성+push**: WS 클라이언트 접속 상태에서 무인증 `POST $B/api/incidents -d '{"siteId":"s1","description":"x"}'` → 201 + `id`; `SELECT status FROM incidents WHERE id=<id>` → `open`; WS에서 `type=crisis_alert` 메시지 1건 수신.
- H. **alertId 멱등(동시성 포함)**: 같은 `alertId`로 순차 2회 POST → 1회차 201, 2회차 200이며 동일 `id` 반환. 또한 같은 `alertId`로 **N(≥12)건을 동시에 POST** → 정확히 **1건만 201**, 나머지 전부 **200**이며 응답 `id`가 모두 동일; **5xx·`SQLITE_BUSY`·UNIQUE 위반 응답 0건**. 두 경우 모두 종료 후 `SELECT COUNT(*) FROM incidents WHERE alert_id='<a>'` → `1`.
- I. **resolved 종착성**: resolve된 사고에 `PATCH .../acknowledge` → 409. 재`PATCH .../resolve` → 409. resolve 시 `resolutionNotes` 공백 → 400.
- J. **센서 해소 fallback**: site `s1`에 미해결 사고 2건일 때 `POST $B/api/incidents/0/resolve-from-sensor -d '{"siteId":"s1","resolvedBy":{"label":"L"}}'` → 200이며 **가장 최근** 사고만 resolved, `resolved_by_kind='sensor_button'`; WS에서 `incident_resolved` 수신.
- K. **장비 자동 영속·부활**: soft-delete된 장비에 대해 `POST $B/api/devices/seen -d '{"siteId":"s1","deviceId":"d1"}'` → 200 후 `SELECT deleted_at FROM devices WHERE site_id='s1' AND device_id='d1'` → `NULL`, `last_seen` 갱신됨.
- L. **재시작 사전 검증**: devices에 없는 `(siteId,deviceId)`로 `POST /api/equipment/restart` → `400` + `{"error": ...}` JSON 본문(미등록 사유); soft-delete된 장비 → `400` + `{"error": ...}` JSON 본문(삭제 사유). 두 경우 모두 hw-gateway 호출이 발생하지 않는다. 상태코드·에러 봉투의 계약 소유자는 `docs/spec/interface-web-api.md`(계약 7)이며, 에러 문구 문자열 자체는 계약이 아니다.
- M. **임시 링크 폐기**: `POST /api/links/temp` → 201의 `token`으로 `GET /api/links/verify/{token}` → 200 `valid:true`; admin이 `DELETE /api/links/{id}` → 204; 같은 verify 재시도 → 401.
- N. **마이그레이션 멱등**: 컨테이너를 2회 연속 기동해도 `SELECT version, COUNT(*) FROM _migrations GROUP BY version HAVING COUNT(*)>1` → 0행, 기동 로그에 마이그레이션 오류 없음.
- O. **건강 전이 이벤트**: 동료 서비스 1개를 내리고 임계 시간 경과 → `SELECT COUNT(*) FROM health_events WHERE entity_id='<svc>' AND status='unhealthy'` 가 정확히 1 증가(폴링이 반복돼도 중복 기록 없음); 서비스 복구 → `healthy` 1건 추가.
- O2. **unhealthy 전이 admin push**: admin WS 클라이언트 접속 상태에서 동료 서비스 1개를 내리고 임계 시간 경과 → 해당 admin WS가 `type=system_alarm` 메시지 1건 수신(payload에 대상 entity 식별과 unhealthy 상태 포함). 같은 조건에서 user(비-admin) WS는 이 메시지를 수신하지 않는다. (자동 컨테이너 재시작은 ⚠️확인요망이므로 단언하지 않는다.)
- P. **admin 시드**: 빈 DB로 첫 기동 후 `SELECT role, status FROM users WHERE username='admin'` → `admin|active` 1행. 재기동해도 중복 생성되지 않는다.
- Q. **토큰 생존성(비밀번호 불변 조건)**: `JWT_SECRET` 미설정 상태에서 로그인 → 컨테이너 재시작 → (해당 사용자가 비밀번호를 변경하지 않았다면) 기존 토큰으로 `GET /api/healthz` → 여전히 200.
- Q2. **비밀번호 변경 시 토큰 무효화**: 사용자 U가 로그인해 토큰 `t`를 얻고 `t`로 `GET /api/incidents` → 200 확인. 이어 U가 `POST /api/auth/change-password`로 비밀번호를 변경 → 이후 **같은 `t`**로 `GET /api/incidents` → `401`; 변경 후 재로그인으로 얻은 새 토큰 → 200. 다른 사용자 V가 변경 전 발급받은 토큰은 영향받지 않아 여전히 200.
- R. **카메라 불변·검증**: 카메라 생성 응답의 `streamKey`는 `cam-[0-9a-f]{8}` 패턴; 이후 어떤 PUT으로도 변하지 않는다. `sourceType:"rtsp"` + `"http://..."` URL → 400; `rtsp://192.168.1.10/...` → 400 (사설망 거부).
- S. **proxy 오류 계약**: recording 컨테이너 정지 상태에서 `GET /api/recordings/<key>` (인증 포함) → `502` + `{"error": ...}` JSON 본문. 상태코드·에러 봉투의 계약 소유자는 `docs/spec/interface-web-api.md`(계약 8)이며, 에러 문구 문자열 자체는 계약이 아니다.
- T. **동시 쓰기 내구성**: 서로 다른(상이 `alertId` 또는 `alertId` 없음) `POST $B/api/incidents`를 **N(≥30)건 동시에** 실행 → **전부 201**, 응답 중 `SQLITE_BUSY`/5xx **0건**, 사고 **유실 0건**; `SELECT COUNT(*) FROM incidents` 가 정확히 N 증가. (WAL + `busy_timeout` 직렬화로 락 경합이 5xx로 새지 않음을 확인.)
- U. **WAL 경계·회수**: 지속 쓰기 부하(예: incidents/devices.seen 반복 POST 수백 회)를 가하는 동안·직후 `$DB-wal`(`/data/sentinel.db-wal`) 크기가 **고정 상한(구현이 정한 체크포인트 임계에 대응, 권장 ≤ 5 MB) 내**에 머물고 부하 진행 중 단조 증가하지 않는다. 부하가 멎고 체크포인트 주기가 지난 뒤 WAL이 **크게 축소**(≈ 초기 수준으로 회수)된다 — 커넥션 풀이 다수 유휴 reader를 상시 보유해도 무한 성장하지 않는다.
- V. **설정 벌크 원자성**: `health.*` 임계 여러 건을 한 벌크 저장 요청으로 보내되 그 중 하나를 미지의 key(또는 무효 값)로 만들어 저장 → `4xx`이며 `SELECT key, value FROM system_settings WHERE key IN (...)`의 **어느 값도 변경 전과 달라지지 않는다**(부분 반영 0건). 같은 요청의 모든 key/값이 유효하면 → 200이며 전 항목이 반영된다. (interface-web-api.md 계약 11과 교차.)

## ⚠️ 리뷰 필요 (의도 불확실)

1. **임시 링크 JWT가 일반 JWT 검증을 통과해 폐기(blacklist) 검사를 우회할 수 있음 — `/api/*` 미들웨어 접면 한정(WS 접면은 해소됨).** 두 토큰 종류가 동일 secret·동일 HS256으로 서명되고, 일반 JWT 파서의 claims 구조가 임시 토큰의 payload(`linkId`만 있고 `userId`/`role` 없음)도 관대하게 수용한다. 인증 미들웨어가 일반 파서를 **먼저** 시도해 성공하면 temp 분기(blacklist 확인)에 도달하지 않으므로, 폐기된 임시 토큰이 `role=""`인 사용자로 `/api/*`를 계속 통과할 수 있어 보인다. 검증 제안: 임시 토큰 발급→폐기 후 `GET /api/incidents`에 그 토큰 사용 — 401이어야 하는데 200이 나오면 NOK. **`/ws` 접면은 이 사안이 계약으로 해소됨** — 접속 시점 temp 식별 + blacklist 확인 + 수립 후 주기적 재검증(만료·회수·비밀번호 변경 경계)으로 폐기된 뷰어의 WS가 능동 종료된다(interface-web-api.md 계약 14, 이슈 #82). 미들웨어 접면의 동일 사안은 여기서 계속 추적한다.
2. **temp 역할의 실효 권한이 "CCTV 열람 전용" 의도를 초과.** 서비스 가이드는 임시 열람자를 CCTV 전용으로 규정하지만, `/api/*` 중 role 검사가 없는 핸들러(사고 목록, 연락처 목록(개인정보), 장비 목록, 건강 상태, 녹화/아카이브 proxy의 **POST·DELETE 포함**, 장비 재시작)는 인증만 통과하면 실행된다. 임시 토큰으로 `POST /api/equipment/restart`나 `DELETE /api/archives/{id}`가 성공한다면 의도 초과다.
3. **`POST /api/links/temp`가 Authorization 헤더 부재 시 완전 무인증 허용.** "헤더가 없으면 내부 서비스 호출"이라는 가정인데, 이 경로는 `/internal/*`이 아닌 `/api/*` 밑에 있어 상위 proxy가 `/api/*`를 외부에 노출하면 누구나 24시간 열람 토큰을 발급받을 수 있다. 네트워크 격리 전제가 이 경로에도 성립하는지 확인 필요.
4. **테스트 알림 경로가 서비스 가이드와 불일치.** 가이드는 `POST /api/test-alert` → notifier(`NOTIFIER_URL`, `/api/notify`) 전달로 기술하나, 코드는 hw-gateway의 `/api/test-alert`로 전달한다. `NOTIFIER_URL`의 실제 용도는 초대 이메일(`/api/send-email`) 뿐이다. 어느 쪽이 현재 의도인지 확정 필요.
5. **연락처 수정의 email 시맨틱 비대칭.** PUT에서 name/phone은 "빈 값이면 기존 유지"인데 email만 요청값으로 무조건 덮어써서, email 필드를 생략(빈 문자열)한 수정 요청이 기존 email을 삭제(NULL)한다. 부분 수정 의도라면 버그성.
6. **SSRF 검사가 raw IP만 차단.** 사설 IP로 resolve되는 도메인명은 통과한다(코드 주석으로 한계 인지됨). 온프레미스 위협 모델에서 허용 가능한지 판단 필요.
7. **rate limiter가 `X-Forwarded-For`를 무조건 신뢰.** 신뢰할 수 있는 proxy 뒤에서만 안전하며, 직접 노출 시 헤더 스푸핑으로 IP별 제한을 우회할 수 있다.
8. **CORS 처리 부재.** 가이드·환경변수(`FRONTEND_URL` — CORS)와 WS 코드 주석("CORS handled elsewhere")은 CORS 처리를 전제하나, 서비스 내에 CORS 헤더를 부여하는 코드가 없고 WS origin 검사는 전부 허용이다. 상위 proxy가 담당하는 구조인지 확인 필요.
9. **alertId 실사용 여부가 문서 간 모순.** 본 스펙 「사고 생명주기」는 "현재 유일 호출자 hw-gateway는 `alertId`를 전송하지 않는다(DB 멱등 경로는 미발화)"라고 기술하나, `docs/spec/interface-web-api.md`(계약 13 호출자 노트)는 "hw-gateway는 crisis forward 시 `alertId`를 전송한다 — DB dedup 경로가 운영에서 실사용된다"라고 반대로 규정한다. 어느 쪽이 현재 의도인지 확정 필요 — 이 결정이 alertId 동시성 원자성(단언 H) 계약의 **운영상 발화 여부**를 좌우한다(전송하지 않으면 H는 계약상 보장이되 실경로 비발화, 전송하면 실경로 필수 보장). 원자성 계약 자체는 호출자와 무관하게 유효하다.
