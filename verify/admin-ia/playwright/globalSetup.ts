import { chromium, type FullConfig } from "@playwright/test";
import * as fs from "fs";
import * as path from "path";
import { login, adminStatePath } from "./_helpers";

// ---------------------------------------------------------------------------
// Playwright globalSetup — runs ONCE before the whole suite.
//
// Logs in as admin a single time and persists the authenticated browser state
// (localStorage token, scoped to the FRONTEND origin) to `.auth/admin.json`.
// playwright.config `use.storageState` then reuses it so per-spec admin logins
// disappear — the web-backend's 10-logins/min-per-IP cap (main.go) is respected
// even when the whole suite is run at once with `npx playwright test`.
// ---------------------------------------------------------------------------
export default async function globalSetup(_config: FullConfig): Promise<void> {
  fs.mkdirSync(path.dirname(adminStatePath), { recursive: true });

  const browser = await chromium.launch();
  try {
    const context = await browser.newContext();
    const page = await context.newPage();
    await login(page); // the single admin login for the entire run
    await context.storageState({ path: adminStatePath });
    await context.close();
  } finally {
    await browser.close();
  }
}
