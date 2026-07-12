import { render, screen, waitFor, act } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import CrisisAlertBanner from "./CrisisAlertBanner";

// Minimal controllable WebSocket stand-in. useWebSocket assigns
// onopen/onmessage/onclose/onerror on the instance; we fire them by hand.
class FakeWebSocket {
  static instances: FakeWebSocket[] = [];
  onopen: (() => void) | null = null;
  onmessage: ((e: { data: string }) => void) | null = null;
  onclose: (() => void) | null = null;
  onerror: (() => void) | null = null;
  url: string;
  constructor(url: string) {
    this.url = url;
    FakeWebSocket.instances.push(this);
  }
  close() {}
}

function crisisFrame(incidentId: string | number, description: string) {
  return {
    data: JSON.stringify({
      type: "crisis_alert",
      payload: {
        incidentId,
        description,
        siteId: "site1",
        occurredAt: "2026-04-13 10:20:30",
      },
      timestamp: "2026-04-13T10:20:30Z",
    }),
  };
}

function activeItem(incidentId: string | number, description: string) {
  return { incidentId, description, siteId: "site1", occurredAt: "2026-04-13 10:20:30" };
}

function resolvedFrame(incidentId: string | number) {
  return {
    data: JSON.stringify({
      type: "incident_resolved",
      payload: {
        incidentId,
        siteId: "site1",
        resolvedAt: "2026-04-13T10:30:00Z",
        resolvedByKind: "web",
        resolvedById: "admin",
        resolvedByLabel: "관리자",
      },
      timestamp: "2026-04-13T10:30:00Z",
    }),
  };
}

let backfill: Array<Record<string, unknown>> = [];

function socket(): FakeWebSocket {
  return FakeWebSocket.instances[FakeWebSocket.instances.length - 1]!;
}

// Fire the current socket's onopen inside act() and let the async backfill
// fetch (.then chain) settle before returning.
async function fireReconnect() {
  const fetchMock = globalThis.fetch as unknown as ReturnType<typeof vi.fn>;
  const before = fetchMock.mock.calls.length;
  await act(async () => {
    socket().onopen?.();
  });
  await waitFor(() => expect(fetchMock.mock.calls.length).toBeGreaterThan(before));
  // flush the resolved-json .then microtasks
  await act(async () => {
    await Promise.resolve();
    await Promise.resolve();
  });
}

beforeEach(() => {
  localStorage.setItem("token", "test-token");
  FakeWebSocket.instances = [];
  backfill = [];
  vi.stubGlobal("WebSocket", FakeWebSocket as unknown as typeof WebSocket);
  vi.stubGlobal(
    "fetch",
    vi.fn(async (url: string) => {
      if (String(url).includes("/api/incidents/active")) {
        return { ok: true, status: 200, json: async () => backfill } as Response;
      }
      return { ok: false, status: 404, json: async () => ({}) } as Response;
    }),
  );
});

afterEach(() => {
  localStorage.clear();
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

describe("CrisisAlertBanner dismissed-memory (#97)", () => {
  it("does NOT resurrect a dismissed, still-unresolved incident on reconnect backfill", async () => {
    render(<CrisisAlertBanner />);
    await fireReconnect(); // initial connect, empty backfill

    // live crisis_alert arrives and is shown
    await act(async () => {
      socket().onmessage?.(crisisFrame("42", "가스 누출"));
    });
    expect(await screen.findByText("가스 누출")).toBeInTheDocument();

    // operator dismisses it
    await userEvent.click(screen.getByLabelText("닫기"));
    await waitFor(() =>
      expect(screen.queryByText("가스 누출")).not.toBeInTheDocument(),
    );

    // reconnect: backfill STILL reports incident 42 as active
    backfill = [activeItem("42", "가스 누출")];
    await fireReconnect();

    // regression assertion: it must stay dismissed (no resurrection)
    expect(screen.queryByText("가스 누출")).not.toBeInTheDocument();
  });

  it("DOES show a genuinely new incidentId after an earlier dismiss", async () => {
    render(<CrisisAlertBanner />);
    await fireReconnect();

    await act(async () => {
      socket().onmessage?.(crisisFrame("42", "가스 누출"));
    });
    await screen.findByText("가스 누출");
    await userEvent.click(screen.getByLabelText("닫기"));
    await waitFor(() =>
      expect(screen.queryByText("가스 누출")).not.toBeInTheDocument(),
    );

    // a NEW incident with a different incidentId fires
    await act(async () => {
      socket().onmessage?.(crisisFrame("77", "화재 감지"));
    });
    expect(await screen.findByText("화재 감지")).toBeInTheDocument();
  });

  it("still backfills a not-yet-dismissed active incident on reconnect (① preserved)", async () => {
    render(<CrisisAlertBanner />);
    await fireReconnect(); // empty

    backfill = [activeItem("99", "질식 위험")];
    await fireReconnect();

    expect(await screen.findByText("질식 위험")).toBeInTheDocument();
  });
});

describe("CrisisAlertBanner live resolve (⚠#3)", () => {
  it("removes the banner on a live incident_resolved over the same socket (no reconnect)", async () => {
    render(<CrisisAlertBanner />);
    await fireReconnect(); // initial connect, empty backfill

    // live crisis_alert arrives and is shown
    await act(async () => {
      socket().onmessage?.(crisisFrame("42", "가스 누출"));
    });
    expect(await screen.findByText("가스 누출")).toBeInTheDocument();

    // incident_resolved for the SAME incidentId over the SAME socket — no reconnect
    await act(async () => {
      socket().onmessage?.(resolvedFrame("42"));
    });
    await waitFor(() =>
      expect(screen.queryByText("가스 누출")).not.toBeInTheDocument(),
    );

    // reconnect backfill still (erroneously) lists 42 as active: dismissedRef
    // gate must keep it from resurrecting.
    backfill = [activeItem("42", "가스 누출")];
    await fireReconnect();
    expect(screen.queryByText("가스 누출")).not.toBeInTheDocument();
  });
});
