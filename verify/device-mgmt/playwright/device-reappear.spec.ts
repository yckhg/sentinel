import { test, expect, type Page } from "@playwright/test";

// ---------------------------------------------------------------------------
// Assertion K (sensor-device-lifecycle spec) — device management UI.
//
// K asserts the browser-observable device-management surface:
//   - "장치 추가" action renders and POSTs /api/devices,
//   - the delete confirmation copy reflects STICKY delete,
//   - a device_reappeared is surfaced with a one-click 재활성 (POST /api/devices),
//   - a lastSeen == null device renders OFFLINE.
//
// DESIGN CAVEAT (preserved intentionally): the frontend surfaces reappearance by
// POLLING /api/devices/all on a 10s setInterval, NOT via a realtime WS frame. This
// harness therefore validates the POLLING UX. Whether the UI should consume the
// realtime WS `device_reappeared` frame instead is a HUMAN PRODUCT DECISION, not a
// harness gap.
// ---------------------------------------------------------------------------

const FRONTEND = process.env.FRONTEND_URL || "http://web-frontend";
const BACKEND = process.env.BACKEND_URL || "http://web-backend:8080";
const ADMIN_USERNAME = process.env.ADMIN_USERNAME || "admin";
const ADMIN_PASSWORD = process.env.ADMIN_PASSWORD || "devverify-admin-pw";
const INTERNAL_TOKEN = process.env.INTERNAL_TOKEN || "devverify-internal-token";

const SITE = "site-devverify";
const DEV = `vs-k-${Date.now()}`; // unique per run → no UNIQUE(site,device) collision on reruns

async function login(page: Page): Promise<string> {
  await page.goto(`${FRONTEND}/login`);
  await page.fill("#login-username", ADMIN_USERNAME);
  await page.fill("#login-password", ADMIN_PASSWORD);
  await page.locator("button.login-submit").click();
  await page.waitForFunction(() => !!localStorage.getItem("token"), null, {
    timeout: 20_000,
  });
  const token = await page.evaluate(() => localStorage.getItem("token"));
  expect(token, "admin JWT should be stored after login").toBeTruthy();
  return token as string;
}

test("K: device management UI — add(→offline)/sticky-delete/reappear-poll/reactivate", async ({
  page,
  request,
}) => {
  // 1) Admin login in the browser.
  const token = await login(page);

  // 2) Open the device management screen (SPA route /admin renders DevicesSection).
  await page.goto(`${FRONTEND}/admin`);
  await expect(page.getByRole("heading", { name: "장비(센서) 관리" })).toBeVisible();

  // 3) "장치 추가" action renders → POST /api/devices (201). New device is offline
  //    (lastSeen == null → 대기).
  await page.getByRole("button", { name: "장치 추가" }).click();
  const addDialog = page.getByRole("dialog", { name: "장치 추가" });
  await expect(addDialog).toBeVisible();
  await addDialog.getByPlaceholder("예: site-001").fill(SITE);
  await addDialog.getByPlaceholder("예: vs-01").fill(DEV);

  const [addResp] = await Promise.all([
    page.waitForResponse(
      (r) => r.url().includes("/api/devices") && r.request().method() === "POST"
    ),
    addDialog.getByRole("button", { name: "추가", exact: true }).click(),
  ]);
  expect(addResp.status(), "POST /api/devices for explicit register → 201").toBe(201);

  // K sub-assertion: lastSeen == null device renders OFFLINE.
  const card = page.locator(".mgmt-card", { hasText: DEV });
  await expect(card).toBeVisible();
  await expect(
    card.locator(".mgmt-card-badge", { hasText: "오프라인" })
  ).toBeVisible();

  // 4) Delete it → confirm modal copy reflects STICKY delete, then confirm.
  await card.getByRole("button", { name: "삭제", exact: true }).click();
  const delDialog = page.getByRole("dialog", { name: "장비 삭제 확인" });
  await expect(delDialog).toBeVisible();
  // Sticky-delete copy (계약 4): re-signal does NOT auto-restore.
  await expect(delDialog).toContainText("자동 복원되지 않습니다");
  await Promise.all([
    page.waitForResponse(
      (r) => r.url().includes("/api/devices/") && r.request().method() === "DELETE"
    ),
    delDialog.getByRole("button", { name: "삭제", exact: true }).click(),
  ]);
  // Deleted device leaves the default (non-deleted) list.
  await expect(page.locator(".mgmt-card", { hasText: DEV })).toHaveCount(0, {
    timeout: 20_000,
  });

  // 5) The deleted device re-signals via POST /api/devices/seen WITH X-Internal-Token
  //    (mirrors hw-gateway). Sticky: it stays deleted but last_seen is refreshed, and
  //    the backend arms device_reappeared.
  const seenResp = await request.post(`${BACKEND}/api/devices/seen`, {
    headers: { "X-Internal-Token": INTERNAL_TOKEN, "Content-Type": "application/json" },
    data: { siteId: SITE, deviceId: DEV },
  });
  expect(seenResp.ok(), `POST /api/devices/seen → 2xx (got ${seenResp.status()})`).toBeTruthy();

  // 6) Wait out the 10s poll interval: the reappear panel must surface the device with
  //    the sticky copy and a 재활성 button.
  const reappear = page.locator(".mgmt-reappear-list .mgmt-notice", { hasText: DEV });
  await expect(reappear).toBeVisible({ timeout: 30_000 });
  await expect(reappear).toContainText("다시 신호를 보냈습니다");
  await expect(reappear).toContainText("삭제 상태는 유지됩니다");
  const reactivateBtn = reappear.getByRole("button", { name: "재활성", exact: true });
  await expect(reactivateBtn).toBeVisible();

  // 7) One-click 재활성 → POST /api/devices (200) → device returns to active.
  const [reactResp] = await Promise.all([
    page.waitForResponse(
      (r) => r.url().includes("/api/devices") && r.request().method() === "POST"
    ),
    reactivateBtn.click(),
  ]);
  expect(reactResp.status(), "reactivation POST /api/devices → 200").toBe(200);

  // Reappear notice for this device clears (it is no longer soft-deleted).
  await expect(
    page.locator(".mgmt-reappear-list .mgmt-notice", { hasText: DEV })
  ).toHaveCount(0, { timeout: 20_000 });

  // Device is back in the active list, no longer marked 삭제됨 (last_seen preserved
  // from the seen → renders 온라인).
  const activeCard = page.locator(".mgmt-card", { hasText: DEV });
  await expect(activeCard).toBeVisible({ timeout: 20_000 });
  await expect(activeCard).not.toContainText("삭제됨");

  // Belt-and-suspenders: confirm via the API that reactivation truly landed
  // (deletedAt cleared) — proves the UI click reactivated, not just hid a notice.
  const listResp = await request.get(`${BACKEND}/api/devices`, {
    headers: { Authorization: `Bearer ${token}` },
  });
  expect(listResp.ok()).toBeTruthy();
  const devices = (await listResp.json()) as Array<{
    siteId: string;
    deviceId: string;
    deletedAt: string | null;
  }>;
  const found = devices.find((d) => d.siteId === SITE && d.deviceId === DEV);
  expect(found, "reactivated device present in GET /api/devices").toBeTruthy();
  expect(found?.deletedAt ?? null, "reactivated device is not soft-deleted").toBeNull();
});
