import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import IncidentsPage from "./IncidentsPage";

function adminToken(): string {
  const b64 = btoa(JSON.stringify({ role: "admin", exp: Math.floor(Date.now() / 1000) + 3600 }))
    .replace(/\+/g, "-")
    .replace(/\//g, "_")
    .replace(/=+$/, "");
  return `h.${b64}.s`;
}

const openIncident = {
  id: 1,
  siteId: "",
  description: "테스트 사고",
  occurredAt: "2025-01-01T00:00:00Z",
  confirmedAt: null,
  confirmedBy: null,
  isTest: false,
  status: "open",
  resolvedAt: null,
  resolvedBy: null,
  resolutionNotes: null,
  resolvedByKind: null,
  resolvedById: null,
  resolvedByLabel: null,
};

beforeEach(() => {
  localStorage.setItem("token", adminToken());
});

afterEach(() => {
  localStorage.clear();
  vi.unstubAllGlobals();
});

describe("IncidentsPage acknowledge error UX (#103)", () => {
  it("shows an inline error (not alert) when acknowledge fails", async () => {
    const alertSpy = vi.fn();
    vi.stubGlobal("alert", alertSpy);
    vi.stubGlobal(
      "fetch",
      vi.fn((_url: string, opts?: RequestInit) => {
        if (opts?.method === "PATCH") {
          return Promise.resolve({
            ok: false,
            status: 500,
            json: () => Promise.resolve({ error: "서버 오류" }),
          } as Response);
        }
        // initial list load
        return Promise.resolve({
          ok: true,
          status: 200,
          json: () =>
            Promise.resolve({
              data: [openIncident],
              pagination: { page: 1, limit: 20, total: 1 },
            }),
        } as Response);
      }),
    );

    const user = userEvent.setup();
    render(<IncidentsPage />);

    const ackBtn = await screen.findByRole("button", { name: "확인" });
    await user.click(ackBtn);

    await waitFor(() => {
      expect(screen.getByText("서버 오류")).toBeInTheDocument();
    });
    expect(alertSpy).not.toHaveBeenCalled();
  });
});
