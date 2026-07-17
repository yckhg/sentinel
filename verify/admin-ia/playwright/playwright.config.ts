import { defineConfig } from "@playwright/test";
import { adminStatePath } from "./_helpers";

// Runs INSIDE the mcr.microsoft.com/playwright container, attached to the
// `adminverify-net` docker network, so service DNS names (web-frontend, web-backend)
// resolve directly. Never point this at prod (3080 / 8080 host ports).
export default defineConfig({
  testDir: ".",
  testMatch: "**/*.spec.ts",
  // Single admin login for the whole run: globalSetup persists the authed state and
  // `use.storageState` seeds it into every context, so the web-backend 10-logins/min
  // rate limit is never tripped when running the full suite at once.
  globalSetup: "./globalSetup.ts",
  // The admin-IA flows cross SPA route transitions plus login round-trips, so give
  // each test generous headroom. `expect` waits cover navigation/render latency.
  timeout: 120_000,
  expect: { timeout: 30_000 },
  fullyParallel: false,
  workers: 1,
  retries: 0,
  reporter: [["list"]],
  use: {
    baseURL: process.env.FRONTEND_URL || "http://web-frontend",
    // Every spec starts already authenticated as admin (localStorage token restored
    // for the FRONTEND origin). Specs that need unauth/non-admin actively clear or
    // replace it via helpers (clearAuthOnOrigin / login as a seeded non-admin).
    storageState: adminStatePath,
    actionTimeout: 15_000,
    navigationTimeout: 30_000,
    trace: "off",
    screenshot: "only-on-failure",
  },
});
