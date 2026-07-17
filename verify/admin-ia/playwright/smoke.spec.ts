import { test, expect } from "@playwright/test";
import { login } from "./_helpers";

// ---------------------------------------------------------------------------
// Placeholder smoke test — proves the adminverify stack + login helper work.
//
// This is INFRA SCAFFOLDING. The real admin-IA routing assertions (A~I) and the
// per-page behavior-preservation specs will be added as separate *.spec.ts files
// alongside this one; they import login/createNonAdminUser from ./_helpers.
// ---------------------------------------------------------------------------

test("smoke: admin can log in and a JWT is stored", async ({ page }) => {
  const token = await login(page);
  expect(token, "admin JWT should be stored after login").toBeTruthy();
});
