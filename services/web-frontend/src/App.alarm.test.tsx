import { render, screen } from "@testing-library/react";
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import App from "./App";

// TDD gate for docs/spec/alarm-history-lifecycle.md assertion K (navigation copy):
// the alarm-history navigation tab label must contain "경보" and must NOT contain
// "사고". Judged by component render (jsdom); the spec declares K needs-browser,
// but the nav label is observable without a live browser, so this is a stronger
// (non-SKIP) gate. RED until the nav copy is renamed 사고→경보.

function makeToken(payload: Record<string, unknown>): string {
  const b64 = btoa(JSON.stringify(payload))
    .replace(/\+/g, "-")
    .replace(/\//g, "_")
    .replace(/=+$/, "");
  return `h.${b64}.s`;
}

class FakeWebSocket {
  onopen: (() => void) | null = null;
  onmessage: ((e: unknown) => void) | null = null;
  onclose: (() => void) | null = null;
  onerror: (() => void) | null = null;
  close(): void {}
}

beforeEach(() => {
  localStorage.setItem(
    "token",
    makeToken({ exp: Math.floor(Date.now() / 1000) + 3600, role: "user" }),
  );
  vi.stubGlobal(
    "fetch",
    vi.fn((url: string) =>
      Promise.resolve({
        ok: true,
        status: 200,
        json: () =>
          Promise.resolve(
            url.includes("/api/incidents")
              ? { data: [], pagination: { page: 1, limit: 20, total: 0 } }
              : [],
          ),
      } as Response),
    ),
  );
  vi.stubGlobal("WebSocket", FakeWebSocket);
});

afterEach(() => {
  localStorage.clear();
  vi.unstubAllGlobals();
});

describe("assertion K — navigation copy is 경보 (not 사고)", () => {
  it("renders an alarm-history nav tab containing 경보 and not 사고", () => {
    render(<App />);

    // The alarm-history tab must be findable by 경보 …
    const alarmTab = screen.getByRole("button", { name: /경보/ });
    expect(alarmTab.textContent).toContain("경보");
    expect(alarmTab.textContent).not.toContain("사고");

    // … and no navigation tab may contain 사고.
    const saoTabs = screen.queryAllByRole("button", { name: /사고/ });
    expect(saoTabs).toHaveLength(0);
  });
});
