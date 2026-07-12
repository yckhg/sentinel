import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import IncidentsPage from "./IncidentsPage";

// TDD gates for docs/spec/alarm-history-lifecycle.md assertions K (page title copy)
// and O (acknowledge UI removal + note-optional ResolveModal), judged by component
// render (jsdom). The spec declares K/O needs-browser SKIP; these render-level
// gates are stronger where jsdom can observe the markup. RED until implemented.

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
  description: "가스 누출 경보",
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

function stubListFetch(patchImpl?: (opts?: RequestInit) => Response) {
  vi.stubGlobal(
    "fetch",
    vi.fn((_url: string, opts?: RequestInit) => {
      if (opts?.method === "PATCH") {
        return Promise.resolve(
          patchImpl
            ? patchImpl(opts)
            : ({ ok: true, status: 200, json: () => Promise.resolve({ status: "resolved" }) } as Response),
        );
      }
      return Promise.resolve({
        ok: true,
        status: 200,
        json: () =>
          Promise.resolve({ data: [openIncident], pagination: { page: 1, limit: 20, total: 1 } }),
      } as Response);
    }),
  );
}

beforeEach(() => {
  localStorage.setItem("token", adminToken());
});

afterEach(() => {
  localStorage.clear();
  vi.unstubAllGlobals();
});

describe("assertion K — page title is 경보 (not 사고)", () => {
  it("renders a page heading containing 경보 and not 사고", async () => {
    stubListFetch();
    render(<IncidentsPage />);

    const heading = await screen.findByRole("heading", { level: 2 });
    expect(heading.textContent).toContain("경보");
    expect(heading.textContent).not.toContain("사고");
  });
});

describe("assertion O — acknowledge UI removed, note optional", () => {
  it("does not render an 확인 (acknowledge) action button", async () => {
    stubListFetch();
    render(<IncidentsPage />);
    // Wait for the list (and its action buttons) to render.
    await screen.findByText("가스 누출 경보");
    expect(screen.queryByRole("button", { name: "확인" })).toBeNull();
  });

  it("does not offer an acknowledged (확인됨) status filter option or label", async () => {
    stubListFetch();
    render(<IncidentsPage />);
    await screen.findByText("가스 누출 경보");
    expect(screen.queryByRole("button", { name: "확인됨" })).toBeNull();
    expect(screen.queryByText("확인됨")).toBeNull();
  });

  it("ResolveModal has no (필수) label and allows submitting an empty note", async () => {
    // PATCH spy: if the empty-note submit is blocked by client-side required
    // validation, no PATCH fetch fires and this stays uncalled (RED).
    const patchSpy = vi.fn(
      () => ({ ok: true, status: 200, json: () => Promise.resolve({ status: "resolved" }) } as Response),
    );
    stubListFetch(patchSpy);

    const user = userEvent.setup();
    render(<IncidentsPage />);
    await screen.findByText("가스 누출 경보");

    // Open the resolve modal (card action button).
    const resolveButtons = screen.getAllByRole("button", { name: "조치 완료" });
    await user.click(resolveButtons[0]);

    // The modal must not label the note field as required.
    await waitFor(() => expect(screen.getByText("조치 완료 처리")).toBeInTheDocument());
    expect(screen.queryByText(/\(필수\)/)).toBeNull();

    // Submit with the note left empty → a PATCH request must be issued.
    const modalButtons = screen.getAllByRole("button", { name: "조치 완료" });
    await user.click(modalButtons[modalButtons.length - 1]);

    await waitFor(() => expect(patchSpy).toHaveBeenCalled());
  });
});
