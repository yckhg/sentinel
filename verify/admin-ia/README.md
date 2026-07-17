# Admin-IA browser verification harness (routing assertions A~I)

Isolated Playwright harness for the **관리(/admin) IA redesign** — the hub + subpage
decomposition. It exists to make the routing/redirect assertions (**A~I**) and the
per-page behavior-preservation checks judgeable in a real browser against a real
backend, without touching prod.

## What this harness is for

The admin-IA redesign splits the monolithic `/admin` screen into a hub plus ten
subpages. This harness drives the SPA + web-backend so specs can assert:

- the **A~I routing/redirect contract** (hub entry, per-subpage routes, redirects,
  admin vs non-admin gating, deep-link/back behavior);
- **behavior preservation** of each moved section (the panels still function after
  being relocated).

Right now the harness ships as **scaffolding only**: a placeholder `smoke.spec.ts`
proves the stack + login helper work. The real A~I / per-page specs are authored on
top of the shared helpers in `_helpers.ts`.

## Isolation invariant (READ BEFORE RUNNING)

This stack is **standalone** and MUST only ever be driven as project `adminverify`. It
is deliberately NOT an overlay of the prod `docker-compose.yml`, because a `-f` overlay
*concatenates* `ports:` — the prod `web-frontend` `3080:80` mapping cannot be removed
and would collide with the running prod container. The standalone file guarantees:

- container names are `adminverify-*` (never `sentinel-*`, never `devverify-*`);
- the only volume is `adminverify-db-data` (never prod `sentinel_db-data`/archives/…);
- the network is a private `adminverify-net`;
- host ports are `38082` (frontend) / `38083` (backend), never prod `3080`, never the
  device-mgmt harness's `38080` / `38081`.

Because the namespace is fully distinct from `devverify-*`, this harness can run
**side by side** with the device-mgmt harness.

**Never `up`/`down` without `-p adminverify`.** Before and after a run, confirm the
prod footprint is untouched:

```bash
docker ps --format '{{.Names}}' | grep '^sentinel-' | sort   # unchanged
```

`down -v` only removes `adminverify-db-data` (the sole volume this project defines).

## Run

From the repo root (`/home/yc/projects/sentinel-admin-ia`):

```bash
# 1) Bring up ONLY web-backend + web-frontend, isolated.
docker compose -p adminverify -f verify/admin-ia/docker-compose.adminverify.yml up -d --build

# 2) Run Playwright IN a container, on the adminverify-net network so service DNS
#    (web-frontend / web-backend) resolves. node_modules installs inside the
#    container (matches the image's bundled browser build, v1.49.1).
docker run --rm --network adminverify-net \
  -v "$PWD/verify/admin-ia/playwright:/work" -w /work \
  -e FRONTEND_URL=http://web-frontend \
  -e BACKEND_URL=http://web-backend:8080 \
  -e ADMIN_USERNAME=admin \
  -e ADMIN_PASSWORD=adminverify-admin-pw \
  -e INTERNAL_TOKEN=adminverify-internal-token \
  -e CI=1 \
  mcr.microsoft.com/playwright:v1.49.1-jammy \
  bash -c "npm install --no-audit --no-fund --silent && npx playwright test"

# 3) Tear down — removes ONLY the adminverify volume.
docker compose -p adminverify -f verify/admin-ia/docker-compose.adminverify.yml down -v
```

Host ports `38082` (frontend) / `38083` (backend) are also published for ad-hoc
manual poking, but the Playwright container reaches the stack via the internal
`adminverify-net` service DNS names, not the host ports.

Fixed test secrets (isolated stack only — never production values) live in
`docker-compose.adminverify.yml`: `JWT_SECRET=adminverify-jwt-secret`,
`ADMIN_USERNAME=admin`, `ADMIN_PASSWORD=adminverify-admin-pw`,
`INTERNAL_TOKEN=adminverify-internal-token`. The same `INTERNAL_TOKEN` is injected
into the Playwright run so any `X-Internal-Token`-gated seeding passes the fail-closed
gate.

## Files

- `docker-compose.adminverify.yml` — standalone isolated stack (backend + frontend).
- `playwright/playwright.config.ts` — Playwright config (baseURL = `http://web-frontend`).
- `playwright/_helpers.ts` — shared helpers: `login`, `createNonAdminUser`, env constants.
- `playwright/smoke.spec.ts` — placeholder smoke test (login → JWT stored).
- `playwright/package.json` — pins `@playwright/test@1.49.1` (matches the image tag).

## Helpers (for A~I spec authors)

`_helpers.ts` exports:

- `login(page, username?, password?) => Promise<string>` — browser login via
  `#login-username` / `#login-password` / `button.login-submit`; waits for
  `localStorage.token`; returns the JWT. Defaults to the admin creds.
- `createNonAdminUser(request, adminToken, {username, password}) => Promise<{id, username, password}>`
  — register (`POST /auth/register`) → admin-approve (`GET /auth/pending` +
  `POST /auth/approve/{id}`) so the user is **active + non-admin** and can log in.
  Pass a unique `username` (e.g. Date.now() suffix) to stay rerun-safe.
- Env constants `FRONTEND`, `BACKEND`, `ADMIN_USERNAME`, `ADMIN_PASSWORD`,
  `INTERNAL_TOKEN` (adminverify defaults).

## Notes

- The frontend nginx has a static `proxy_pass http://streaming:8080` upstream that
  must resolve at startup; `web-backend` advertises an extra `streaming` network alias
  to satisfy it (the `/live/` route is never exercised by these tests).
