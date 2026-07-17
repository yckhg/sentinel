import { test, expect, type Page } from "@playwright/test";
import { login, createNonAdminUser, FRONTEND } from "./_helpers";

// ---------------------------------------------------------------------------
// Seam gate specs [seam-A]~[seam-I] — the admin routing/navigation接합부 contract
// (docs/spec/admin-routing-navigation-contract.md, SSOT). Each test = one seam
// assertion ID; selectors follow verify/admin-ia/MOUNT-CONTRACT.md.
//
// TDD RED gate: authored BEFORE the master (hub + routing) implementation. Most
// assertions are expected to fail now — the master turns them GREEN.
//
// Observation model (per task): deep-link/refresh via page.goto/page.reload;
// browser back via page.goBack(); drilldown via clicking admin-hub-link; the
// seam owns the判정 by enumerating all 10 slugs.
// ---------------------------------------------------------------------------

// The exactly-10 slug allowlist (from the seam contract & MOUNT-CONTRACT).
const SLUGS = [
  "devices",
  "cameras",
  "health",
  "contacts",
  "test-alert",
  "notify-test",
  "system",
  "users",
  "storage",
  "cctv-links",
] as const;

// The 4 top-level tabs that must remain unchanged by this redesign.
const TABS: { label: string; path: string }[] = [
  { label: "CCTV", path: "/cctv" },
  { label: "경보이력", path: "/incidents" },
  { label: "관리", path: "/admin" },
  { label: "설정", path: "/settings" },
];

const subpage = (page: Page, slug: string) =>
  page.locator(`[data-testid="admin-page"][data-slug="${slug}"]`);

// [seam-A] /admin direct entry mounts the hub (admin-hub renders).
test("[seam-A] /admin 직접 진입 시 관리 허브가 마운트된다", async ({ page }) => {
  await login(page);
  await page.goto(`${FRONTEND}/admin`);
  await expect(page).toHaveURL(/\/admin$/);
  await expect(page.getByTestId("admin-hub")).toBeVisible();
});

// [seam-B] For each of the 10 slugs, /admin/<slug> deep-link renders a subpage
// (not the hub, not blank/crash) and URL == /admin/<slug> is preserved.
test("[seam-B] 10 slug 각각 딥링크 시 서브페이지 렌더 + URL 유지", async ({
  page,
}) => {
  test.setTimeout(180_000);
  await login(page);
  for (const slug of SLUGS) {
    await page.goto(`${FRONTEND}/admin/${slug}`);
    await expect(page, `URL stays /admin/${slug}`).toHaveURL(
      new RegExp(`/admin/${slug.replace("-", "\\-")}$`)
    );
    await expect(
      subpage(page, slug),
      `admin-page[data-slug=${slug}] renders`
    ).toBeVisible();
    // Not the hub.
    await expect(page.getByTestId("admin-hub")).toHaveCount(0);
  }
});

// [seam-C] In-page back affordance always returns to /admin regardless of entry
// path (normative). Plus browser-back returns to hub on drilldown, and the router
// seeds /admin in history on a deep-link first entry.
test("[seam-C] 페이지 내 뒤로 어포던스·브라우저 back 모두 허브로 복귀", async ({
  page,
}) => {
  test.setTimeout(180_000);
  await login(page);

  // (1) Normative mechanism: admin-back → /admin for every slug (deep-link entry).
  for (const slug of SLUGS) {
    await page.goto(`${FRONTEND}/admin/${slug}`);
    await page.getByTestId("admin-back").click();
    await expect(page, `admin-back from ${slug} → /admin`).toHaveURL(/\/admin$/);
    await expect(page.getByTestId("admin-hub")).toBeVisible();
  }

  // (2) Browser back after drilldown → hub.
  await page.goto(`${FRONTEND}/admin`);
  await page.locator(`[data-testid="admin-hub-link"][data-slug="devices"]`).click();
  await expect(page).toHaveURL(/\/admin\/devices$/);
  await page.goBack();
  await expect(page).toHaveURL(/\/admin$/);
  await expect(page.getByTestId("admin-hub")).toBeVisible();

  // (3) Browser back after a deep-link first entry → seeded /admin.
  await page.goto(`${FRONTEND}/admin/cameras`);
  await page.goBack();
  await expect(page).toHaveURL(/\/admin$/);
  await expect(page.getByTestId("admin-hub")).toBeVisible();
});

// [seam-D] Selecting a hub item changes URL to /admin/<slug> and renders that
// subpage (drilldown), enumerated over all 10 slugs.
test("[seam-D] 허브 항목 선택 시 /admin/<slug> 이동 + 서브페이지 렌더", async ({
  page,
}) => {
  test.setTimeout(180_000);
  await login(page);
  for (const slug of SLUGS) {
    await page.goto(`${FRONTEND}/admin`);
    await page
      .locator(`[data-testid="admin-hub-link"][data-slug="${slug}"]`)
      .click();
    await expect(page, `drilldown ${slug} URL`).toHaveURL(
      new RegExp(`/admin/${slug.replace("-", "\\-")}$`)
    );
    await expect(subpage(page, slug)).toBeVisible();
  }
});

// [seam-E] Route-level role==admin gate: non-admin and unauthenticated fixtures
// entering /admin or any /admin/<slug> get (i) NO admin content and (ii) a
// role-deterministic destination — unauth → /login?returnTo=<orig>, authed
// non-admin → /cctv (replace). Both observations judged, over /admin + 10 slugs.
test("[seam-E] 게이트: 미인증→login?returnTo·non-admin→/cctv + 관리콘텐츠 부재", async ({
  page,
  request,
}) => {
  test.setTimeout(300_000);
  const paths = ["/admin", ...SLUGS.map((s) => `/admin/${s}`)];

  // -- Unauthenticated fixture: no login, just goto.
  for (const p of paths) {
    await page.goto(`${FRONTEND}${p}`);
    await page.waitForURL(
      (u) => u.pathname === "/login" && u.searchParams.get("returnTo") === p,
      { timeout: 15_000 }
    );
    await expect(page.getByTestId("admin-hub"), `no hub for unauth ${p}`).toHaveCount(0);
    await expect(page.getByTestId("admin-page"), `no admin-page for unauth ${p}`).toHaveCount(0);
  }

  // -- Authenticated non-admin fixture: created via real register→approve flow.
  const adminToken = await login(page);
  const nonAdmin = await createNonAdminUser(request, adminToken, {
    username: `nonadmin_${Date.now()}`,
    password: "NonAdmin-pw-123",
  });
  // Log out (clear token) then log in as the non-admin.
  await page.evaluate(() => localStorage.removeItem("token"));
  await login(page, nonAdmin.username, nonAdmin.password);

  for (const p of paths) {
    await page.goto(`${FRONTEND}${p}`);
    await page.waitForURL((u) => u.pathname === "/cctv", { timeout: 15_000 });
    await expect(page.getByTestId("admin-hub"), `no hub for non-admin ${p}`).toHaveCount(0);
    await expect(page.getByTestId("admin-page"), `no admin-page for non-admin ${p}`).toHaveCount(0);
  }
});

// [seam-F] /admin/<unknown> (slug ∉ allowlist) → existing 404 fallback
// (data-view=not-found) and the 4-tab bar is NOT rendered.
test("[seam-F] 허용목록 밖 /admin/<unknown>는 not-found 폴백 + 탭바 미렌더", async ({
  page,
}) => {
  await login(page);
  for (const unknown of ["nonesuch", "bogus", "devicesx"]) {
    await page.goto(`${FRONTEND}/admin/${unknown}`);
    await expect(
      page.locator('[data-view="not-found"]'),
      `not-found for /admin/${unknown}`
    ).toBeVisible();
    await expect(page.locator(".tab-bar")).toHaveCount(0);
    await expect(page.getByTestId("admin-page")).toHaveCount(0);
  }
});

// [seam-G] returnTo allowlist guard: a /admin/<slug> returnTo is honored after
// login; an external/arbitrary URL is rejected (falls back to default /cctv).
test("[seam-G] returnTo 허용목록: /admin/<slug> 허용, 외부 URL 거부", async ({
  page,
}) => {
  test.setTimeout(180_000);

  // Manual form fill (not the login() helper) because we must supply the
  // returnTo query on the /login URL — the helper always hits bare /login.
  const loginWithReturnTo = async (returnTo: string) => {
    await page.evaluate(() => localStorage.removeItem("token"));
    await page.goto(`${FRONTEND}/login?returnTo=${encodeURIComponent(returnTo)}`);
    await page.fill("#login-username", process.env.ADMIN_USERNAME || "admin");
    await page.fill(
      "#login-password",
      process.env.ADMIN_PASSWORD || "adminverify-admin-pw"
    );
    await page.locator("button.login-submit").click();
  };

  // Allowed: each /admin/<slug> is honored.
  for (const slug of SLUGS) {
    await loginWithReturnTo(`/admin/${slug}`);
    await expect(page, `returnTo /admin/${slug} honored`).toHaveURL(
      new RegExp(`/admin/${slug.replace("-", "\\-")}$`)
    );
  }

  // Rejected: external / arbitrary URLs → default /cctv.
  for (const evil of ["https://evil.example.com", "//evil.example.com", "javascript:alert(1)"]) {
    await loginWithReturnTo(evil);
    await expect(page, `external returnTo rejected → /cctv`).toHaveURL(/\/cctv$/);
  }
});

// [seam-H] The 4 top-level tabs and their canonical paths remain present &
// functional after the redesign.
test("[seam-H] 상위 4탭(cctv·incidents·admin·settings) 존재·동작", async ({
  page,
}) => {
  await login(page);
  await page.goto(`${FRONTEND}/cctv`);
  await expect(page.locator(".tab-bar button")).toHaveCount(4);
  for (const { label, path } of TABS) {
    await page.getByRole("button", { name: label }).click();
    await expect(page, `tab ${label} → ${path}`).toHaveURL(
      new RegExp(`${path.replace("/", "\\/")}$`)
    );
  }
});

// [seam-I] Refresh (reload) on any /admin/<slug> restores the same subpage
// (URL == SSOT), enumerated over all 10 slugs.
test("[seam-I] 서브페이지 새로고침 후 동일 서브페이지 복원", async ({ page }) => {
  test.setTimeout(180_000);
  await login(page);
  for (const slug of SLUGS) {
    await page.goto(`${FRONTEND}/admin/${slug}`);
    await expect(subpage(page, slug)).toBeVisible();
    await page.reload();
    await expect(page, `URL after reload ${slug}`).toHaveURL(
      new RegExp(`/admin/${slug.replace("-", "\\-")}$`)
    );
    await expect(
      subpage(page, slug),
      `admin-page[data-slug=${slug}] restored after reload`
    ).toBeVisible();
  }
});
