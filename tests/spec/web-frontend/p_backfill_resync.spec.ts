import { test, expect } from "@playwright/test";
import {
  injectToken,
  installWsMock,
  installActiveStub,
  stubJson,
  type WsMock,
  type ActiveStub,
} from "./_fixtures";

// 단언 P — 재접속 백필 재동기화 (/api/incidents/active, crisis_alert 동형).
// spec: web-frontend.md §검증 단언 P / §출력 3-(c); interface-web-api.md §계약2 /api/incidents/active.

const SITE = {
  address: "서울시 강남구 테헤란로 1",
  managerName: "홍길동",
  managerPhone: "010-1234-5678",
};

function activeItem(id: number, status: "open" | "acknowledged") {
  return {
    incidentId: id,
    siteId: "site-001",
    description: `위기 ${id}`,
    occurredAt: "2026-07-11 10:20:30",
    isTest: false,
    status,
    site: SITE,
  };
}

function livePush(id: number) {
  return {
    type: "crisis_alert",
    payload: {
      incidentId: id,
      siteId: "site-001",
      description: `라이브 위기 ${id}`,
      occurredAt: "2026-07-11 09:00:00",
      site: SITE,
    },
    timestamp: "2026-07-11T09:00:00Z",
  };
}

const banners = (page: import("@playwright/test").Page) => page.locator(".crisis-banner");
const banner = (page: import("@playwright/test").Page, id: number) =>
  page.locator(`.crisis-banner[data-incident-id="${id}"]`);
const marker = (page: import("@playwright/test").Page) => page.locator("[data-ws-state]");

async function reconnect(ws: WsMock, page: import("@playwright/test").Page) {
  // Clean disconnect then let the socket come back (outage stays false).
  await ws.drop();
  await expect(marker(page)).toHaveAttribute("data-ws-state", "connected", { timeout: 15_000 });
}

test.describe("P. 재접속 백필 재동기화", () => {
  let ws: WsMock;
  let active: ActiveStub;

  test.beforeEach(async ({ page }) => {
    await injectToken(page);
    await stubJson(page, "**/api/cameras", []);
    active = await installActiveStub(page);
    ws = await installWsMock(page);
    active.body = []; // initial connect backfill = empty
    await page.goto("/cctv");
    await expect(marker(page)).toHaveAttribute("data-ws-state", "connected");
    await expect(banners(page)).toHaveCount(0);
  });

  test("P-1: 추가 + 주소 fallback 파생표시", async ({ page }) => {
    active.body = [activeItem(12, "open")];
    await reconnect(ws, page);

    await expect(banner(page, 12)).toBeVisible();
    await expect(banners(page)).toHaveCount(1);

    // 파생표시: 백필 site.address가 119 다이얼로그 주소 fallback으로 채워짐 (출력 10).
    // Geolocation is not granted → GPS fails → dialog shows site.address.
    await banner(page, 12).locator(".emergency-call-btn").click();
    const value = page.locator(".emergency-dialog-value");
    await expect(value).toBeVisible();
    await expect(value).not.toHaveText("위치 확인 중...");
    await expect(value).toHaveText(SITE.address);
  });

  test("P-2: acknowledged 위기도 배너 포함", async ({ page }) => {
    active.body = [activeItem(21, "acknowledged")];
    await reconnect(ws, page);
    await expect(banner(page, 21)).toBeVisible();
    await expect(banners(page)).toHaveCount(1);
  });

  test("P-3: 중복 없음 (dedup 키 = incidentId)", async ({ page }) => {
    // 라이브 push로 배너 33 표시.
    await ws.push(livePush(33));
    await expect(banner(page, 33)).toBeVisible();
    await expect(banners(page)).toHaveCount(1);
    // 백필이 같은 incidentId 반환 → 중복 추가 금지.
    active.body = [activeItem(33, "open")];
    await reconnect(ws, page);
    await expect(banners(page)).toHaveCount(1);
    await expect(banner(page, 33)).toHaveCount(1);
  });

  test("P-4: 스테일 제거 (진짜 sync)", async ({ page }) => {
    await ws.push(livePush(44));
    await expect(banner(page, 44)).toBeVisible();
    // 끊긴 구간에 resolved → 백필 응답에 없음 → 제거.
    active.body = [];
    await reconnect(ws, page);
    await expect(banners(page)).toHaveCount(0);
  });

  test("P-5: 표시기 분리 (백필 5xx여도 connected + 기존 배너 유지)", async ({ page }) => {
    await ws.push(livePush(55));
    await expect(banner(page, 55)).toBeVisible();
    // 백필 접면 오류.
    active.status = 500;
    active.body = { error: "boom" };
    await reconnect(ws, page); // reconnect() already asserts connected on socket open
    await expect(marker(page)).toHaveAttribute("data-ws-state", "connected");
    // 기존 배너 집합 임의 소거 금지.
    await expect(banner(page, 55)).toBeVisible();
    await expect(banners(page)).toHaveCount(1);
  });
});
