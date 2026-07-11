import { test, expect } from "@playwright/test";
import { injectToken, installWsMock, stubJson, TOKEN } from "./_fixtures";

// 단언 Q — URL 라우팅·딥링크 (canonical /cctv·/incidents·/admin·/settings).
// spec: web-frontend.md §검증 단언 Q / §핵심로직 라우팅·returnTo.

// authed-common noise suppression
async function quietApis(page: import("@playwright/test").Page) {
  await installWsMock(page);
  // Playwright routes are LIFO — register the broad catch-all FIRST so the
  // specific stubs below win.
  await page.route("**/api/**", (route) =>
    route.fulfill({ status: 200, contentType: "application/json", body: "[]" })
  );
  await stubJson(page, "**/api/cameras", []);
  await stubJson(page, "**/api/incidents/active", []);
  // IncidentsPage reads the {data, pagination} envelope — [] would crash render.
  await stubJson(page, "**/api/incidents*", {
    data: [],
    pagination: { page: 1, limit: 20, total: 0 },
  });
}

test.describe("Q. URL 라우팅·딥링크", () => {
  test("Q-1: 비-CCTV 탭(/admin) 딥링크 + 새로고침 복원 (CCTV 리셋 아님)", async ({ page }) => {
    await injectToken(page);
    await quietApis(page);
    await page.goto("/admin");
    await expect(page).toHaveURL(/\/admin$/);
    await expect(page.locator(".tab-item.active .tab-label")).toHaveText("관리");

    await page.reload();
    await expect(page).toHaveURL(/\/admin$/);
    await expect(page.locator(".tab-item.active .tab-label")).toHaveText("관리");
  });

  test("Q-2: 탭 이동 후 뒤로가기 → 직전 탭", async ({ page }) => {
    await injectToken(page);
    await quietApis(page);
    await page.goto("/cctv");
    await expect(page).toHaveURL(/\/cctv$/);
    await page.getByRole("button", { name: /관리/ }).click();
    await expect(page).toHaveURL(/\/admin$/);
    await page.goBack();
    await expect(page).toHaveURL(/\/cctv$/);
    await expect(page.locator(".tab-item.active .tab-label")).toHaveText("CCTV");
  });

  test("Q-3: 미인증 보호경로 접근 → /login, URL이 보호경로로 안 남음", async ({ page }) => {
    await installWsMock(page); // no token injected
    await page.goto("/admin");
    await expect(page).toHaveURL(/\/login/);
    expect(new URL(page.url()).pathname).toBe("/login");
    expect(new URL(page.url()).searchParams.get("returnTo")).toBe("/admin");
    await expect(page.locator(".login-title")).toBeVisible();
  });

  test("Q-4: 미매칭 경로(/nonexistent) → 404 (메인앱 흡수 아님)", async ({ page }) => {
    await injectToken(page);
    await quietApis(page);
    await page.goto("/nonexistent");
    await expect(page.locator('[data-view="not-found"]')).toBeVisible();
    await expect(page.locator(".not-found-code")).toHaveText("404");
    expect(new URL(page.url()).pathname).toBe("/nonexistent");
    // 흡수 안 됨: 탭바(메인앱) 렌더되지 않음.
    await expect(page.locator(".tab-bar")).toHaveCount(0);
  });

  test("Q-5: canonical 경로 문자열 정확", async ({ page }) => {
    await injectToken(page);
    await quietApis(page);
    await page.goto("/cctv");
    const cases: [RegExp, RegExp][] = [
      [/사고이력/, /\/incidents$/],
      [/관리/, /\/admin$/],
      [/설정/, /\/settings$/],
      [/CCTV/, /\/cctv$/],
    ];
    for (const [label, url] of cases) {
      await page.getByRole("button", { name: label }).click();
      await expect(page).toHaveURL(url);
    }
  });

  test("Q-6: returnTo 복귀 + 무효 fallback(/cctv)", async ({ page }) => {
    // 미인증 /admin 딥링크 → /login → 로그인 성공 → /admin 복귀.
    await installWsMock(page);
    await stubJson(page, "**/auth/login", { token: TOKEN });
    await page.route("**/api/**", (route) =>
      route.fulfill({ status: 200, contentType: "application/json", body: "[]" })
    );
    await page.goto("/admin");
    await expect(page).toHaveURL(/\/login\?returnTo=%2Fadmin/);
    await page.getByPlaceholder("아이디를 입력하세요").fill("admin");
    await page.getByPlaceholder("비밀번호를 입력하세요").fill("sentinel1234");
    await page.locator("button.login-submit").click();
    await expect(page).toHaveURL(/\/admin$/);

    // returnTo 무효(앱 밖) → /cctv.
    await page.evaluate(() => window.localStorage.removeItem("token"));
    await page.goto("/login?returnTo=https://evil.example/x");
    await page.getByPlaceholder("아이디를 입력하세요").fill("admin");
    await page.getByPlaceholder("비밀번호를 입력하세요").fill("sentinel1234");
    await page.locator("button.login-submit").click();
    await expect(page).toHaveURL(/\/cctv$/);
  });

  test("Q-7: history replace — 복귀 후 뒤로가기가 /login으로 안 감", async ({ page }) => {
    await installWsMock(page);
    await stubJson(page, "**/auth/login", { token: TOKEN });
    await page.route("**/api/**", (route) =>
      route.fulfill({ status: 200, contentType: "application/json", body: "[]" })
    );
    await page.goto("/admin");
    await expect(page).toHaveURL(/\/login\?returnTo=%2Fadmin/);
    await page.getByPlaceholder("아이디를 입력하세요").fill("admin");
    await page.getByPlaceholder("비밀번호를 입력하세요").fill("sentinel1234");
    await page.locator("button.login-submit").click();
    await expect(page).toHaveURL(/\/admin$/);

    await page.goBack().catch(() => {});
    // 루프 방지: 뒤로가기가 /login·중간항목으로 되돌아가지 않음.
    expect(new URL(page.url()).pathname).not.toBe("/login");
  });
});
