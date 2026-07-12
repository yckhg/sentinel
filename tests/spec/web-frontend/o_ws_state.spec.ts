import { test, expect } from "@playwright/test";
import { injectToken, installWsMock, stubJson } from "./_fixtures";

// 단언 O — 연결 상태 가시성 (data-ws-state 삼상태, 소켓 상태만).
// spec: web-frontend.md §검증 단언 O / §출력 3-(a).

test.describe("O. 연결 상태 가시성", () => {
  test.beforeEach(async ({ page }) => {
    await injectToken(page);
    // Keep the app quiet: empty cameras + empty backfill.
    await stubJson(page, "**/api/cameras", []);
    await stubJson(page, "**/api/incidents/active", []);
  });

  test("O: connected → disconnect(reconnecting) → reconnect(connected), 탭 전환에도 유지", async ({
    page,
  }) => {
    const ws = await installWsMock(page);
    await page.goto("/cctv");

    const marker = page.locator("[data-ws-state]");

    // (1) 마커 존재 + connected + 문구.
    await expect(marker).toHaveCount(1);
    await expect(marker).toHaveAttribute("data-ws-state", "connected");
    await expect(marker).toHaveClass(/ws-status-connected/);
    const connectedText = (await marker.locator(".ws-status-text").textContent())?.trim();
    expect(connectedText).toBe("실시간 연결됨");

    // (2) 강제 절단 → connected 이탈(reconnecting/disconnected) + 색·문구 변화.
    ws.setOutage(true); // pin the socket down so the transient state is observable
    await ws.drop();
    await expect(marker).toHaveAttribute("data-ws-state", /disconnected|reconnecting/);
    await expect(marker).not.toHaveClass(/ws-status-connected/);
    const downText = (await marker.locator(".ws-status-text").textContent())?.trim();
    expect(downText).not.toBe(connectedText); // 문구가 실제로 바뀜
    expect(["연결 끊김", "재접속 중..."]).toContain(downText);

    // (3) 재접속(소켓 open) 즉시 connected 복귀 (백필 무관).
    ws.setOutage(false);
    await expect(marker).toHaveAttribute("data-ws-state", "connected", { timeout: 15_000 });
    await expect(marker).toHaveClass(/ws-status-connected/);

    // (4) 어느 탭에서도 마커 DOM 유지 — 관리 탭으로 이동해도 존재.
    await page.getByRole("button", { name: /관리/ }).click();
    await expect(page).toHaveURL(/\/admin$/);
    await expect(page.locator("[data-ws-state]")).toHaveCount(1);
    await expect(page.locator("[data-ws-state]")).toHaveAttribute("data-ws-state", "connected");
  });
});
