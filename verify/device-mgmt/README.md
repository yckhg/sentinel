# Device-management browser verification (assertion K)

Isolated Playwright harness that makes the **load-bearing SKIP assertion K** of
`docs/spec/sensor-device-lifecycle.md` judgeable — the frontend's `device_reappeared`
reactivation UI.

## What K validates here

The `DevicesSection` component (web-frontend) surfaces device reappearance by
**POLLING `/api/devices/all` on a 10s `setInterval`** (see `POLL_INTERVAL_MS`), NOT by
consuming a realtime WebSocket frame. This harness therefore validates the **polling
UX**:

1. admin login in the browser;
2. **장치 추가** → `POST /api/devices` (201); the new device renders **오프라인**
   (`lastSeen == null`);
3. delete it → the confirm dialog shows the **sticky-delete** copy
   ("…자동 복원되지 않습니다…"); the device leaves the active list;
4. `POST /api/devices/seen` with `X-Internal-Token` (mirrors hw-gateway) re-signals the
   deleted device;
5. after the poll interval the **reappear panel** renders the device with the sticky
   copy ("…다시 신호를 보냈습니다. 삭제 상태는 유지됩니다…") and a **재활성** button;
6. click **재활성** → `POST /api/devices` (200) → device returns to active
   (confirmed via UI **and** a `GET /api/devices` `deletedAt == null` check).

### DESIGN CAVEAT (human decision, not a harness gap)

Because the UI surfaces reappearance by **polling**, K validates the **polling flow**.
Whether the UI should instead consume the realtime WS `device_reappeared` frame is a
**HUMAN PRODUCT DECISION**, not something this harness can (or should) decide.

## Isolation invariant (READ BEFORE RUNNING)

This stack is **standalone** and MUST only ever be driven as project `devverify`. It is
deliberately NOT an overlay of the prod `docker-compose.yml`, because a `-f` overlay
*concatenates* `ports:` — the prod `web-frontend` `3080:80` mapping cannot be removed
and would collide with the running prod container. The standalone file guarantees:

- container names are `devverify-*` (never `sentinel-*`);
- the only volume is `devverify-db-data` (never prod `sentinel_db-data`/archives/…);
- the network is a private `devverify-net`;
- host ports are `38080` (frontend) / `38081` (backend), never prod `3080`.

**Never `up`/`down` without `-p devverify`.** Before and after a run, confirm the prod
footprint is untouched:

```bash
docker ps --format '{{.Names}}' | grep '^sentinel-' | sort      # unchanged, 10 containers
docker volume ls -q | grep -v devverify | sort > /tmp/vols.txt  # identical before/after
```

`down -v` only removes `devverify-db-data` (the sole volume this project defines).

## Run

From the repo root (`/home/yc/projects/sentinel`):

```bash
# 1) Bring up ONLY web-backend + web-frontend, isolated.
docker compose -p devverify -f verify/device-mgmt/docker-compose.devverify.yml up -d --build

# 2) Run Playwright IN a container, on the devverify-net network so service DNS
#    (web-frontend / web-backend) resolves. node_modules installs inside the
#    container (matches the image's bundled browser build, v1.49.1).
docker run --rm --network devverify-net \
  -v "$PWD/verify/device-mgmt/playwright:/work" -w /work \
  -e FRONTEND_URL=http://web-frontend \
  -e BACKEND_URL=http://web-backend:8080 \
  -e ADMIN_USERNAME=admin \
  -e ADMIN_PASSWORD=devverify-admin-pw \
  -e INTERNAL_TOKEN=devverify-internal-token \
  -e CI=1 \
  mcr.microsoft.com/playwright:v1.49.1-jammy \
  bash -c "npm install --no-audit --no-fund --silent && npx playwright test"

# 3) Tear down — removes ONLY the devverify volume.
docker compose -p devverify -f verify/device-mgmt/docker-compose.devverify.yml down -v
```

Fixed test secrets (isolated stack only — never production values) live in
`docker-compose.devverify.yml`: `JWT_SECRET`, `ADMIN_USERNAME=admin`,
`ADMIN_PASSWORD=devverify-admin-pw`, `INTERNAL_TOKEN=devverify-internal-token`. The
same `INTERNAL_TOKEN` is injected into the Playwright run so the `seen` call passes the
fail-closed `X-Internal-Token` gate.

## Files

- `docker-compose.devverify.yml` — standalone isolated stack (backend + frontend).
- `playwright/playwright.config.ts` — Playwright config (baseURL = `http://web-frontend`).
- `playwright/device-reappear.spec.ts` — the K test.
- `playwright/package.json` — pins `@playwright/test@1.49.1` (matches the image tag).

## Notes

- The frontend nginx has a static `proxy_pass http://streaming:8080` upstream that must
  resolve at startup; `web-backend` advertises an extra `streaming` network alias to
  satisfy it (the `/live/` route is never exercised by K).
- The default sensor alive threshold is 60s (`health.sensor_alive_threshold_sec`), well
  above the 10s poll, so a just-`seen` device reliably reads as alive in the reappear
  panel — no faked assertion, no shortened timer.
