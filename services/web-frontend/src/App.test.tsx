import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import App from "./App";

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

describe("App tab bar a11y (#100)", () => {
  it("marks the active tab with aria-current and moves it on navigation", async () => {
    const user = userEvent.setup();
    render(<App />);

    const cctvTab = screen.getByRole("button", { name: /CCTV/ });
    expect(cctvTab).toHaveAttribute("aria-current", "page");

    const incidentsTab = screen.getByRole("button", { name: /경보이력/ });
    expect(incidentsTab).not.toHaveAttribute("aria-current");

    await user.click(incidentsTab);
    expect(incidentsTab).toHaveAttribute("aria-current", "page");
    expect(cctvTab).not.toHaveAttribute("aria-current");
  });
});
