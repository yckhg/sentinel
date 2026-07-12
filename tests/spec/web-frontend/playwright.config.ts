import { defineConfig, devices } from "@playwright/test";

// Browser gates for web-frontend 단언 O·P·Q·R.
// Target the LIVE web-frontend container over the sentinel docker network
// (default http://web-frontend:80) or host http://localhost:3080.
export default defineConfig({
  testDir: ".",
  testMatch: /[opqr]_.*\.spec\.ts$/,
  timeout: 60_000,
  expect: { timeout: 10_000 },
  fullyParallel: false,
  workers: 1,
  retries: 0,
  reporter: [["list"]],
  use: {
    baseURL: process.env.BASE_URL || "http://web-frontend:80",
    headless: true,
    actionTimeout: 15_000,
    navigationTimeout: 20_000,
    ignoreHTTPSErrors: true,
    // geolocation intentionally NOT granted → getCurrentPosition errors →
    // dialog falls back to site.address (단언 P-1 / 출력 10).
    permissions: [],
  },
  projects: [{ name: "chromium", use: { ...devices["Desktop Chrome"] } }],
});
