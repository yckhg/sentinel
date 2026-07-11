// Shared fixtures for web-frontend browser gates (단언 O·P·Q·R).
// spec: docs/spec/web-frontend.md §검증 단언, docs/spec/interface-web-api.md §계약8.
//
// Auth: the frontend verifies ONLY the JWT `exp` claim client-side (no signature
// check — access control lives in the backend). So a standard-base64 payload with
// a future exp is accepted by isTokenExpired(). We inject it into localStorage.
import type { Page, WebSocketRoute } from "@playwright/test";

function b64(obj: unknown): string {
  // Standard base64 (with +/=) so the browser's atob() decodes it (isTokenExpired).
  return Buffer.from(JSON.stringify(obj)).toString("base64");
}

const header = b64({ alg: "HS256", typ: "JWT" });
const payload = b64({
  sub: "admin",
  username: "admin",
  role: "admin",
  exp: Math.floor(Date.now() / 1000) + 3600,
});
export const TOKEN = `${header}.${payload}.spectdd-sig`;

// Inject a valid (future-exp) token into localStorage before app scripts run.
export async function injectToken(page: Page): Promise<void> {
  await page.addInitScript((t) => {
    window.localStorage.setItem("token", t);
  }, TOKEN);
}

// A controllable WebSocket mock. Without connectToServer() the page socket is
// auto-accepted (client `open` fires → wsState "connected").
//  - drop()        : server-side close → client reconnect loop (backoff 1s..8s)
//  - setOutage(b)  : while true, every (re)connect is closed immediately →
//                    state pinned at "reconnecting"
//  - push(msg)     : server → client message (e.g. crisis_alert)
export interface WsMock {
  push(msg: unknown): Promise<void>;
  drop(): Promise<void>;
  setOutage(v: boolean): void;
}

export async function installWsMock(page: Page): Promise<WsMock> {
  const state: { outage: boolean; current: WebSocketRoute | null } = {
    outage: false,
    current: null,
  };
  await page.routeWebSocket(/\/ws(\?|$)/, (ws) => {
    state.current = ws;
    if (state.outage) ws.close();
  });
  return {
    async push(msg: unknown) {
      state.current?.send(typeof msg === "string" ? msg : JSON.stringify(msg));
    },
    async drop() {
      const c = state.current;
      if (c) await c.close();
    },
    setOutage(v: boolean) {
      state.outage = v;
    },
  };
}

// Mutable JSON stub for the reconnect-backfill접면 GET /api/incidents/active.
export interface ActiveStub {
  status: number;
  body: unknown;
}
export async function installActiveStub(page: Page): Promise<ActiveStub> {
  const ref: ActiveStub = { status: 200, body: [] };
  await page.route("**/api/incidents/active", (route) =>
    route.fulfill({
      status: ref.status,
      contentType: "application/json",
      body: JSON.stringify(ref.body),
    })
  );
  return ref;
}

// Generic JSON fulfiller.
export async function stubJson(
  page: Page,
  pattern: string,
  body: unknown,
  status = 200
): Promise<void> {
  await page.route(pattern, (route) =>
    route.fulfill({
      status,
      contentType: "application/json",
      body: JSON.stringify(body),
    })
  );
}
