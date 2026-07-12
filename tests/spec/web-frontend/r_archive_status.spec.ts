import { test, expect, type Page } from "@playwright/test";
import { injectToken, installWsMock, stubJson } from "./_fixtures";

// 단언 R — 아카이브 status 소비자 판정 (/api/archives → RecordingTimeline).
// spec: web-frontend.md §검증 단언 R; interface-web-api.md §계약8 아카이브 status 소비자 계약.

const STREAM = "cam1";

function archive(id: string, status: string, extra: Record<string, unknown> = {}) {
  return {
    id,
    incidentId: "",
    streamKey: STREAM,
    from: "2026-07-11T00:00:00Z",
    to: "2026-07-11T00:05:00Z",
    createdAt: "2026-07-11T00:05:10Z",
    sizeBytes: 0,
    filePath: "",
    status,
    error: "",
    ...extra,
  };
}

const ARCHIVES = [
  archive("a-protecting", "protecting"),
  archive("a-pending", "pending"),
  archive("a-finalizing", "finalizing"),
  archive("a-processing", "processing"),
  archive("a-completed", "completed", { sizeBytes: 5 * 1024 * 1024 }),
  archive("a-failed", "failed", { error: "ffmpeg 인코딩 실패" }),
  archive("a-unknown", "weird-unknown-status"),
];

// Navigate into /cctv, expand the single camera, open the archives list.
async function openArchives(page: Page) {
  await injectToken(page);
  await installWsMock(page);
  await stubJson(page, "**/api/incidents/active", []);
  await stubJson(page, "**/api/incidents*", { data: [], pagination: { total: 0 } });
  await stubJson(page, "**/api/cameras", [
    {
      id: 1,
      name: "Cam A",
      location: "L",
      zone: "A",
      hlsUrl: "/live/cam1/index.m3u8",
      streamKey: STREAM,
      status: "connected",
      siteId: "s1",
      deviceId: "d1",
    },
  ]);
  await stubJson(page, `**/api/recordings/${STREAM}`, { timeRanges: [] });
  await stubJson(page, "**/api/archives", ARCHIVES);

  await page.goto("/cctv");
  await page.locator(".camera-cell").first().click(); // expand → mounts RecordingTimeline
  const toggle = page.locator(".rec-timeline-archives-toggle");
  await expect(toggle).toBeVisible();
  await toggle.click();
  await expect(page.locator(".rec-timeline-archives-list")).toBeVisible();
}

// Per-item locator: the archive item that contains the given status marker.
function itemWith(page: Page, state: string) {
  return page.locator(".rec-timeline-archive-item", {
    has: page.locator(`[data-archive-state="${state}"]`),
  });
}

test.describe("R. 아카이브 status 소비자 판정", () => {
  test("R-1: 6종 전부 구별 (오인 없음)", async ({ page }) => {
    await openArchives(page);
    const canonical = ["protecting", "pending", "finalizing", "processing", "completed", "failed"];

    // 각 status가 자기 자신의 data-archive-state로 렌더 (서로 다른 상태로 오인 안 됨).
    const rendered: string[] = [];
    for (const s of canonical) {
      await expect(page.locator(`[data-archive-state="${s}"]`)).toHaveCount(1);
      rendered.push(s);
    }
    // 값들이 모두 서로 구별됨.
    expect(new Set(rendered).size).toBe(canonical.length);

    // completed만 다운로드 어피어던스, 나머지 미완료 상태엔 없음 (완료로 오인 금지).
    await expect(itemWith(page, "completed").locator(".rec-timeline-archive-download")).toHaveCount(1);
    for (const s of ["protecting", "pending", "finalizing", "processing"]) {
      await expect(itemWith(page, s).locator(".rec-timeline-archive-download")).toHaveCount(0);
    }
  });

  test("R-2: 미지 상태 안전 fallback (미완료·다운로드 없음·completed 아님)", async ({ page }) => {
    await openArchives(page);
    const item = itemWith(page, "unknown");
    await expect(item).toHaveCount(1);
    // 완료로 표시되지 않음.
    await expect(page.locator('[data-archive-state="completed"]', {
      hasText: "미완료",
    })).toHaveCount(0);
    await expect(item.locator('[data-archive-state="unknown"]')).toHaveText("미완료(진행 중)");
    // 다운로드 어피어던스 없음.
    await expect(item.locator(".rec-timeline-archive-download")).toHaveCount(0);
  });

  test("R-3: failed 오류 종단 (라벨+사유, 다운로드 없음)", async ({ page }) => {
    await openArchives(page);
    const item = itemWith(page, "failed");
    await expect(item).toHaveCount(1);
    await expect(item.locator('[data-archive-state="failed"]')).toHaveText("실패");
    await expect(item.locator(".rec-timeline-archive-error")).toContainText("ffmpeg 인코딩 실패");
    await expect(item.locator(".rec-timeline-archive-download")).toHaveCount(0);
  });
});
