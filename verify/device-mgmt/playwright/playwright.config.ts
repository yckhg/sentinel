import { defineConfig } from "@playwright/test";

// Runs INSIDE the mcr.microsoft.com/playwright container, attached to the
// `devverify-net` docker network, so service DNS names (web-frontend, web-backend)
// resolve directly. Never point this at prod (3080 / 8080 host ports).
export default defineConfig({
  testDir: ".",
  // The K flow crosses a 10s frontend poll interval plus a login round-trip, so give
  // the single test generous headroom. `expect` waits cover the poll latency.
  timeout: 120_000,
  expect: { timeout: 30_000 },
  fullyParallel: false,
  workers: 1,
  retries: 0,
  reporter: [["list"]],
  use: {
    baseURL: process.env.FRONTEND_URL || "http://web-frontend",
    actionTimeout: 15_000,
    navigationTimeout: 30_000,
    trace: "off",
    screenshot: "only-on-failure",
  },
});
