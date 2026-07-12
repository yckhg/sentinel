import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import HealthPanel from "./HealthPanel";

// -----------------------------------------------------------------------------
// Gate for docs/spec/system-status-aggregate.md — 요약 창(시스템 상태 패널)
// client contract. The panel must consume the NEW aggregate endpoint
// GET /api/health/summary (요약 카운트 + 고정 서비스 목록 + 예외 장비만),
// NOT the flat GET /api/health array. It must also:
//   - render the summary counts (healthy/abnormal/offline)
//   - render the full service list with status
//   - render ONLY exception (abnormal/offline) devices — healthy devices never
//     appear as rows (boundary invariant J); show an overflow indicator when
//     exceptionsOverflow > 0
//   - provide a device search (assertion D) that looks up current status via
//     the devices接면 (GET /api/devices...)
//   - drill down on an exception into that device's transition history via
//     GET /api/health/events?entity_id=<siteId:deviceId> (assertion E)
//   - observe camera connection status separately from GET /api/cameras
//     (assertion G — outside the aggregate response shape)
//
// These are RED until HealthPanel is reworked to consume /api/health/summary.
// -----------------------------------------------------------------------------

function adminToken(): string {
  const b64 = btoa(JSON.stringify({ role: "admin", exp: Math.floor(Date.now() / 1000) + 3600 }))
    .replace(/\+/g, "-")
    .replace(/\//g, "_")
    .replace(/=+$/, "");
  return `h.${b64}.s`;
}

function jsonRes(data: unknown, ok = true, status = 200): Promise<Response> {
  return Promise.resolve({
    ok,
    status,
    json: () => Promise.resolve(data),
  } as Response);
}

interface SummaryFixture {
  summary: { healthy: number; abnormal: number; offline: number };
  services: { id: string; status: "healthy" | "unhealthy" }[];
  exceptions: { id: string; displayName: string; category: string; ageSec: number; reason: string }[];
  exceptionsOverflow: number;
}

const services = [
  { id: "hw-gateway", status: "healthy" as const },
  { id: "cctv-adapter", status: "healthy" as const },
  { id: "youtube-adapter", status: "healthy" as const },
  { id: "streaming", status: "unhealthy" as const },
  { id: "recording", status: "healthy" as const },
  { id: "notifier", status: "healthy" as const },
];

// 200 healthy but only 2 exceptions — the boundary-invariant scenario (J).
const bigFixture: SummaryFixture = {
  summary: { healthy: 200, abnormal: 1, offline: 1 },
  services,
  exceptions: [
    { id: "s:AB-0", displayName: "AB-0", category: "abnormal", ageSec: 5, reason: "alert active" },
    { id: "s:OFF-0", displayName: "OFF-0", category: "offline", ageSec: 3600, reason: "no heartbeat" },
  ],
  exceptionsOverflow: 0,
};

const cameras = [
  { id: 1, name: "cam-connected", status: "connected" },
  { id: 2, name: "cam-offline", status: "disconnected" },
];

// installFetch routes by URL. summary is checked BEFORE the generic /api/health
// fallback because "/api/health/summary" also contains "/api/health".
function installFetch(summary: SummaryFixture, extra?: (url: string, opts?: RequestInit) => Promise<Response> | undefined) {
  const fetchMock = vi.fn((url: string, opts?: RequestInit) => {
    const u = String(url);
    const custom = extra?.(u, opts);
    if (custom) return custom;
    if (u.includes("/api/health/summary")) return jsonRes(summary);
    if (u.includes("/api/health/events")) return jsonRes([]);
    if (u.includes("/api/cameras")) return jsonRes(cameras);
    if (u.includes("/api/devices")) return jsonRes([]);
    if (u.includes("/api/health")) return jsonRes([]); // legacy flat endpoint
    return jsonRes({});
  });
  vi.stubGlobal("fetch", fetchMock);
  return fetchMock;
}

function calledWith(fetchMock: ReturnType<typeof vi.fn>, fragment: string): boolean {
  return fetchMock.mock.calls.some((c) => String(c[0]).includes(fragment));
}

beforeEach(() => {
  localStorage.setItem("token", adminToken());
});

afterEach(() => {
  localStorage.clear();
  vi.unstubAllGlobals();
});

describe("시스템 상태 패널 — /api/health/summary 집계 소비 (spec system-status-aggregate)", () => {
  it("집계 엔드포인트를 소비하고 요약 카운트 + 서비스 전체를 렌더한다 (F/J)", async () => {
    const fetchMock = installFetch(bigFixture);
    render(<HealthPanel />);

    // Full fixed service set is always present with status.
    await screen.findByText(/hw-gateway/);
    for (const svc of services) {
      expect(screen.getAllByText(new RegExp(svc.id)).length).toBeGreaterThan(0);
    }

    // Consumed the aggregate endpoint, not the flat /api/health array.
    expect(calledWith(fetchMock, "/api/health/summary")).toBe(true);

    // Summary counts are rendered (healthy 200 / abnormal 1 / offline 1).
    const body = document.body.textContent || "";
    expect(body).toMatch(/200/);
  });

  it("예외(이상/오프라인) 장비만 나열하고 정상 장비는 행으로 나오지 않는다 (C/J)", async () => {
    installFetch(bigFixture);
    render(<HealthPanel />);

    // The two exception devices appear.
    await waitFor(() => {
      expect(screen.getAllByText(/AB-0/).length).toBeGreaterThan(0);
      expect(screen.getAllByText(/OFF-0/).length).toBeGreaterThan(0);
    });

    // Healthy devices are represented only by the count — they are never itemized.
    // (The fixture has 200 healthy devices but none are in exceptions.)
    expect(screen.queryByText(/H-0|HEALTHY|OK-0/)).toBeNull();
  });

  it("예외 수가 상한을 초과하면 오버플로 표식을 보여준다", async () => {
    const overflow: SummaryFixture = {
      ...bigFixture,
      summary: { healthy: 10, abnormal: 0, offline: 55 },
      exceptionsOverflow: 5,
    };
    installFetch(overflow);
    render(<HealthPanel />);

    await screen.findByText(/AB-0/);
    const body = document.body.textContent || "";
    expect(body).toMatch(/5/);
    expect(body).toMatch(/외|초과|남|더|more|overflow/i);
  });

  it("장비 검색 입력이 존재하고 검색 시 현재-상태 조회(/api/devices)를 호출한다 (D)", async () => {
    const fetchMock = installFetch(bigFixture, (u) => {
      if (u.includes("/api/devices")) {
        return jsonRes({ id: 7, siteId: "s", deviceId: "OK-9", alias: "", lastSeen: "2026-07-12T00:00:00Z", alertState: "none" });
      }
      return undefined;
    });
    render(<HealthPanel />);
    await screen.findByText(/hw-gateway/);

    const user = userEvent.setup();
    const boxes = screen.getAllByRole("textbox");
    expect(boxes.length).toBeGreaterThan(0); // a search input exists (J)
    await user.type(boxes[0]!, "s:OK-9{Enter}");

    // A search button may also drive it — click one if present.
    const searchBtn = screen.queryByRole("button", { name: /검색|search|조회/i });
    if (searchBtn) await user.click(searchBtn);

    await waitFor(() => {
      expect(calledWith(fetchMock, "/api/devices")).toBe(true);
    });
  });

  it("예외 항목 드릴다운 시 그 장비의 events?entity_id 이력을 조회한다 (E)", async () => {
    const fetchMock = installFetch(bigFixture);
    render(<HealthPanel />);

    const target = await screen.findByText(/OFF-0/);
    const user = userEvent.setup();
    // Click the exception row/element to drill down into its history.
    const clickable = target.closest("button") || target.closest("[role='button']") || target;
    await user.click(clickable as Element);

    await waitFor(() => {
      const hit = fetchMock.mock.calls.some((c) => {
        const u = String(c[0]);
        // GET /api/health/events?entity_id=<siteId:deviceId> — ":" may be
        // percent-encoded as %3A, so match on the device fragment.
        return u.includes("/api/health/events") && u.includes("entity_id=") && u.includes("OFF-0");
      });
      expect(hit).toBe(true);
    });
  });

  it("카메라 연결 상태는 /api/cameras에서 별도로 관측한다 (G)", async () => {
    const fetchMock = installFetch(bigFixture);
    render(<HealthPanel />);
    await screen.findByText(/hw-gateway/);
    await waitFor(() => {
      expect(calledWith(fetchMock, "/api/cameras")).toBe(true);
    });
  });
});

// Silence unused-import warnings if a helper goes unused during iteration.
void within;
