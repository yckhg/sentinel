import { test, expect, type Page } from "@playwright/test";
import {
  login,
  createNonAdminUser,
  readAdminToken,
  clearAuthOnOrigin,
  FRONTEND,
} from "./_helpers";

// Auth model: playwright.config `use.storageState` seeds every test already
// authenticated as admin (single globalSetup login), so admin-only specs no longer
// log in. Only hub-F clears that state (unauth path) and logs in once as a seeded
// non-admin, keeping the whole-suite login count under the backend's 10/min cap.

// ---------------------------------------------------------------------------
// Hub gate specs [hub-A]~[hub-G] — the admin hub contract
// (docs/spec/admin-hub.md). Each test = one hub assertion ID; selectors follow
// verify/admin-ia/MOUNT-CONTRACT.md (admin-hub / admin-hub-group[data-group] /
// admin-hub-link[data-slug] / admin-page[data-slug] / admin-back).
//
// TDD RED gate: authored BEFORE the master implementation; RED now is expected.
// Observation model: goto /admin then read DOM markers; drilldown via clicking
// admin-hub-link; return via admin-back. Gate/drilldown-reach assertions (F/E/G)
// are judged at INTEGRATION time (master routing in place).
// ---------------------------------------------------------------------------

// Group label → its exact item slug set (from the admin-hub spec output table).
const GROUPS: Record<string, string[]> = {
  "장치": ["devices", "cameras", "health"],
  "알림·연락": ["contacts", "test-alert", "notify-test"],
  "시스템": ["system", "users"],
  "저장·CCTV": ["storage", "cctv-links"],
};

const ALL_SLUGS = Object.values(GROUPS).flat(); // exactly 10, each once.

const slugsFromLinks = async (page: Page, scope = ""): Promise<string[]> => {
  const sel = `${scope} [data-testid="admin-hub-link"]`.trim();
  return page.locator(sel).evaluateAll((els) =>
    els.map((e) => e.getAttribute("data-slug") || "")
  );
};

// [hub-A] admin at /admin sees the hub with all 4 group labels present.
test("[hub-A] 허브 렌더 + 4개 그룹 라벨 존재", async ({ page }) => {
  // Already admin-authed via storageState.
  await page.goto(`${FRONTEND}/admin`);
  await expect(page.getByTestId("admin-hub")).toBeVisible();
  await expect(page.getByTestId("admin-hub-group")).toHaveCount(4);
  for (const label of Object.keys(GROUPS)) {
    await expect(
      page.locator(`[data-testid="admin-hub-group"][data-group="${label}"]`),
      `group label "${label}" present`
    ).toBeVisible();
  }
});

// [hub-B] Exactly 10 function items are listed (none missing).
test("[hub-B] 허브에 10개 기능 항목이 모두 나열", async ({ page }) => {
  // Already admin-authed via storageState.
  await page.goto(`${FRONTEND}/admin`);
  await expect(page.getByTestId("admin-hub-link")).toHaveCount(10);
});

// [hub-C] Each item sits under its exact group (group→item set matches the table).
test("[hub-C] 그룹별 항목 집합이 스펙 표와 일치", async ({ page }) => {
  // Already admin-authed via storageState.
  await page.goto(`${FRONTEND}/admin`);
  for (const [label, expectedSlugs] of Object.entries(GROUPS)) {
    const scope = `[data-testid="admin-hub-group"][data-group="${label}"]`;
    const got = (await slugsFromLinks(page, scope)).sort();
    expect(got, `group "${label}" item set`).toEqual([...expectedSlugs].sort());
  }
});

// [hub-D] Item↔slug 1:1 — every slug appears exactly once, no two items share a
// target, and the full set equals the 10-slug allowlist.
test("[hub-D] 항목↔slug 1:1 · 전 slug 정확히 1회", async ({ page }) => {
  // Already admin-authed via storageState.
  await page.goto(`${FRONTEND}/admin`);
  const got = await slugsFromLinks(page);
  expect(got.slice().sort(), "all slugs present exactly once").toEqual(
    ALL_SLUGS.slice().sort()
  );
  expect(new Set(got).size, "no duplicate slug targets").toBe(got.length);
});

// [hub-E] Activating an item navigates to /admin/<slug> and reaches that
// subpage (drilldown result; routing正本 owned by seam D, judged at integration).
test("[hub-E] 항목 활성화 시 /admin/<slug> 도달 + admin-page 렌더", async ({
  page,
}) => {
  test.setTimeout(180_000);
  // Already admin-authed via storageState.
  for (const slug of ALL_SLUGS) {
    await page.goto(`${FRONTEND}/admin`);
    await page
      .locator(`[data-testid="admin-hub-link"][data-slug="${slug}"]`)
      .click();
    await expect(page, `reach /admin/${slug}`).toHaveURL(
      new RegExp(`/admin/${slug.replace("-", "\\-")}$`)
    );
    await expect(
      page.locator(`[data-testid="admin-page"][data-slug="${slug}"]`)
    ).toBeVisible();
  }
});

// [hub-F] A non-admin (non-admin role OR unauthenticated) at /admin does NOT see
// the hub item list and is routed by the existing gate (seam E smoke reference;
// 正본 판정 is seam-owned).
test("[hub-F] 비-admin은 허브 항목 미렌더 + 게이트 유도", async ({
  page,
  request,
}) => {
  test.setTimeout(180_000);

  // Unauthenticated: drop the storageState-seeded admin token first (on a real
  // origin — never about:blank), then no hub items rendered; redirected to login.
  await clearAuthOnOrigin(page);
  await page.goto(`${FRONTEND}/admin`);
  await page.waitForURL(
    (u) => u.pathname === "/login" && u.searchParams.get("returnTo") === "/admin",
    { timeout: 15_000 }
  );
  await expect(page.getByTestId("admin-hub-link")).toHaveCount(0);
  await expect(page.getByTestId("admin-hub")).toHaveCount(0);

  // Authenticated non-admin: no hub items rendered; redirected to /cctv. Reuse the
  // shared admin bearer (from storageState) for API seeding — no admin login here.
  const adminToken = readAdminToken();
  const nonAdmin = await createNonAdminUser(request, adminToken, {
    username: `nonadmin_${Date.now()}`,
    password: "NonAdmin-pw-123",
  });
  await login(page, nonAdmin.username, nonAdmin.password);
  await page.goto(`${FRONTEND}/admin`);
  await page.waitForURL((u) => u.pathname === "/cctv", { timeout: 15_000 });
  await expect(page.getByTestId("admin-hub-link")).toHaveCount(0);
  await expect(page.getByTestId("admin-hub")).toHaveCount(0);
});

// [hub-G] Returning to the hub from any subpage (admin-back) re-observes the
// same 10-item grouped list (seam C smoke reference; judged at integration).
test("[hub-G] 서브페이지에서 허브 복귀 시 10개 항목 재관측", async ({ page }) => {
  test.setTimeout(180_000);
  // Already admin-authed via storageState.
  for (const slug of ["devices", "cctv-links"]) {
    await page.goto(`${FRONTEND}/admin/${slug}`);
    await page.getByTestId("admin-back").click();
    await expect(page).toHaveURL(/\/admin$/);
    await expect(page.getByTestId("admin-hub")).toBeVisible();
    await expect(page.getByTestId("admin-hub-link")).toHaveCount(10);
    const got = await slugsFromLinks(page);
    expect(got.slice().sort(), "10 items re-observed").toEqual(
      ALL_SLUGS.slice().sort()
    );
  }
});
