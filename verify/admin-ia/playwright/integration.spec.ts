import { test, expect } from "@playwright/test";
import { FRONTEND } from "./_helpers";

// ---------------------------------------------------------------------------
// Integration gate — CROSS-UNIT regression, runs after the master + all 10 leaves
// are merged. Distinct from the per-leaf static/logic verification: this drives the
// fully-assembled /admin in a real browser against the real backend (adminverify).
//
// It proves what the per-leaf pass could NOT: every real subpage actually mounts at
// its canonical path (the master stub was REPLACED, not left behind), the back
// affordance survives relocation, and hub drilldown reaches each real page. Seam A~I
// and hub A~G run alongside this file in the same suite, so routing/gate contracts
// are re-judged against the real (non-stub) pages too.
//
// Auth: the whole suite runs as admin via the shared storageState (see globalSetup).
// ---------------------------------------------------------------------------

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

// The master shipped every subpage as a stub whose body was the literal sentinel
// below. A real leaf REPLACES that body. So "the page renders and does NOT contain
// this sentinel" is a robust, uniform proof that the real content mounted.
const STUB_SENTINEL = "구현 예정";

test("[integration] 10 슬러그 딥링크 → 실 콘텐츠(스텁 아님) + admin-back 보존", async ({
  page,
}) => {
  for (const slug of SLUGS) {
    await page.goto(`${FRONTEND}/admin/${slug}`);

    // Identity anchor: a subpage (not hub/blank/crash) for exactly this slug.
    const root = page.locator(`[data-testid="admin-page"][data-slug="${slug}"]`);
    await expect(root, `/admin/${slug} mounts its subpage`).toBeVisible();

    // Real content, not the master stub.
    await expect(
      page.getByText(STUB_SENTINEL),
      `/admin/${slug} should be the real leaf, not the "${STUB_SENTINEL}" stub`
    ).toHaveCount(0);

    // Back affordance survived the relocation (seam-C normative mechanism).
    await expect(
      page.getByTestId("admin-back"),
      `/admin/${slug} keeps the back-to-hub affordance`
    ).toBeVisible();

    // URL is still the SSOT for this slug.
    await expect(page).toHaveURL(new RegExp(`/admin/${slug.replace("-", "\\-")}$`));
  }
});

test("[integration] 허브 드릴다운 왕복 → 각 실 서브페이지 도달 후 허브 복귀", async ({
  page,
}) => {
  for (const slug of SLUGS) {
    await page.goto(`${FRONTEND}/admin`);
    await expect(page.getByTestId("admin-hub")).toBeVisible();

    await page.locator(`[data-testid="admin-hub-link"][data-slug="${slug}"]`).click();

    // Reached the real subpage.
    await expect(page).toHaveURL(new RegExp(`/admin/${slug.replace("-", "\\-")}$`));
    await expect(
      page.locator(`[data-testid="admin-page"][data-slug="${slug}"]`)
    ).toBeVisible();
    await expect(page.getByText(STUB_SENTINEL)).toHaveCount(0);

    // Back to hub via the in-page affordance.
    await page.getByTestId("admin-back").click();
    await expect(page).toHaveURL(/\/admin$/);
    await expect(page.getByTestId("admin-hub")).toBeVisible();
  }
});

// A couple of high-signal feature markers, to confirm the assembled pages carry
// their distinctive UI (not merely a non-stub shell). These two ship an explicit
// leaf-owned testid; the rest are covered by the non-stub check above + per-leaf
// static verification.
test("[integration] 대표 기능 마커 존재 (test-alert·notify-test)", async ({ page }) => {
  await page.goto(`${FRONTEND}/admin/test-alert`);
  await expect(page.getByTestId("test-alert-trigger")).toBeVisible();

  await page.goto(`${FRONTEND}/admin/notify-test`);
  await expect(page.getByTestId("notify-test")).toBeVisible();
});
