import { test, expect } from "@playwright/test";
import { installWsMock } from "./_fixtures";

// 단언 Q-6·Q-7 — 실자격 로그인 회귀 가드 (LIVE web-backend, /auth/login NON-stub).
// spec: web-frontend.md §미인증 딥링크·복귀(returnTo) / §검증 단언 Q(6)(7).
//
// WHY a separate gate: q_url_routing.spec.ts STUBS `**/auth/login` with a canned
// token via route.fulfill. That intercept resolves in-process almost instantly,
// so it does NOT reproduce the real race the fix addresses: a genuine network
// round-trip to the live backend returns the token asynchronously, the auth
// state (isAuthed) updates, and — pre-flushSync — the protected-route guard
// could observe a STALE isAuthed=false and bounce the user back to /login.
// Here we drive the REAL POST /auth/login round-trip (admin/sentinel1234) so the
// gate is RED on the pre-flushSync code (되튕김) and GREEN on the current fix.
//
// Discipline: login is a harmless READ round-trip (token issuance). After login
// we observe ROUTING/URL ONLY — no mutating ops (사고확인/조치/재시작) whatsoever.

const USER = "admin";
const PASS = "sentinel1234";

// Quiet the authed data APIs (cameras/incidents/...) so the authed views render
// deterministically. Crucially we do NOT touch /auth/login — that hits the live
// backend for real. WebSocket is mocked (auto-accept) to avoid live-socket noise.
async function quietDataApis(page: import("@playwright/test").Page) {
  await installWsMock(page);
  // Playwright routes are LIFO — broad catch-all first, specific stubs win.
  await page.route("**/api/**", (route) =>
    route.fulfill({ status: 200, contentType: "application/json", body: "[]" })
  );
  await page.route("**/api/incidents*", (route) =>
    route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ data: [], pagination: { page: 1, limit: 20, total: 0 } }),
    })
  );
}

async function realLogin(page: import("@playwright/test").Page) {
  await expect(page.locator(".login-title")).toBeVisible();
  await page.getByPlaceholder("아이디를 입력하세요").fill(USER);
  await page.getByPlaceholder("비밀번호를 입력하세요").fill(PASS);
  // Assert the submit triggers a REAL POST to /auth/login (no stub).
  const [resp] = await Promise.all([
    page.waitForResponse(
      (r) => r.url().includes("/auth/login") && r.request().method() === "POST"
    ),
    page.locator("button.login-submit").click(),
  ]);
  expect(resp.status()).toBe(200);
}

test.describe("Q(real). 실자격 로그인 회귀 가드 (라이브 백엔드, 되튕김 부재)", () => {
  test("Q-real-1: 실로그인 → /cctv 진입, 수 초 후에도 되튕김 없음 (authed 앱 렌더)", async ({
    page,
  }) => {
    await quietDataApis(page); // NO token injected → 미인증 진입
    await page.goto("/cctv");
    // 미인증 보호경로 → /login (returnTo 보존)
    await expect(page).toHaveURL(/\/login\?returnTo=%2Fcctv/);

    await realLogin(page);

    // 실 라운드트립 후 /cctv 진입.
    await expect(page).toHaveURL(/\/cctv$/);
    // authed 앱 렌더 확인: 로그인 폼이 아니라 메인앱(탭바) 렌더.
    await expect(page.locator(".tab-bar")).toBeVisible();
    await expect(page.locator(".login-title")).toHaveCount(0);
    await expect(page.locator(".tab-item.active .tab-label")).toHaveText("CCTV");

    // 되튕김 부재의 결정적 관측: 수 초간 pathname이 /cctv를 유지하고
    // /login으로 되돌아가지 않는다 (stale isAuthed=false 경쟁 재현 시 여기서 RED).
    for (let i = 0; i < 6; i++) {
      await page.waitForTimeout(500);
      expect(new URL(page.url()).pathname).toBe("/cctv");
    }
    await expect(page.locator(".tab-bar")).toBeVisible();
  });

  test("Q-real-2: 미인증 /admin 딥링크 → /login(returnTo) → 실로그인 → /admin 복귀", async ({
    page,
  }) => {
    await quietDataApis(page); // 미인증
    await page.goto("/admin");
    // 미인증 보호경로 → /login?returnTo=%2Fadmin (URL이 /admin으로 안 남음)
    await expect(page).toHaveURL(/\/login\?returnTo=%2Fadmin/);
    expect(new URL(page.url()).pathname).toBe("/login");
    expect(new URL(page.url()).searchParams.get("returnTo")).toBe("/admin");

    await realLogin(page);

    // 실 라운드트립 후 /admin 복귀 — 경량 로그인 폼이 아니라 authed /admin 뷰.
    await expect(page).toHaveURL(/\/admin$/);
    await expect(page.locator(".tab-bar")).toBeVisible();
    await expect(page.locator(".login-title")).toHaveCount(0);
    await expect(page.locator(".tab-item.active .tab-label")).toHaveText("관리");

    // 복귀 후 되튕김 부재 재확인.
    for (let i = 0; i < 6; i++) {
      await page.waitForTimeout(500);
      expect(new URL(page.url()).pathname).toBe("/admin");
    }
  });

  test("Q-real-3: history replace — 복귀 후 뒤로가기가 /login·리다이렉트 중간항목이 아님", async ({
    page,
  }) => {
    await quietDataApis(page); // 미인증
    await page.goto("/admin");
    await expect(page).toHaveURL(/\/login\?returnTo=%2Fadmin/);

    await realLogin(page);
    await expect(page).toHaveURL(/\/admin$/);
    await expect(page.locator(".tab-bar")).toBeVisible();

    // 뒤로가기 → /login·리다이렉트 중간항목으로 되돌아가지 않는다 (루프 없음).
    await page.goBack().catch(() => {});
    await page.waitForTimeout(500);
    expect(new URL(page.url()).pathname).not.toBe("/login");
  });
});
