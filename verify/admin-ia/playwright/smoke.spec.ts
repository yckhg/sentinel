import { test, expect } from "@playwright/test";
import { readAdminToken, FRONTEND } from "./_helpers";

// ---------------------------------------------------------------------------
// Placeholder smoke test — proves the adminverify stack + the shared admin auth
// work end-to-end.
//
// This is INFRA SCAFFOLDING. It no longer performs its own /auth/login: the single
// globalSetup admin login persists an authenticated storageState (localStorage
// token) that playwright.config seeds into every context, so the whole suite stays
// under the web-backend's 10-logins/min-per-IP cap. The smoke assertion is that this
// shared auth is present and actually renders an authenticated admin page.
// ---------------------------------------------------------------------------

test("smoke: shared admin auth (storageState) reaches an authed admin page", async ({
  page,
}) => {
  expect(readAdminToken(), "admin JWT persisted by globalSetup").toBeTruthy();
  await page.goto(`${FRONTEND}/admin`);
  await expect(page.getByTestId("admin-hub")).toBeVisible();
});
