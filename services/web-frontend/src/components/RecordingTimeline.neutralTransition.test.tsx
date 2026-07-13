import { render, screen, fireEvent, act, within } from "@testing-library/react";
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import RecordingTimeline from "./RecordingTimeline";

// Executable B7 regression gate for the neutral-transition lifecycle
// (docs/spec/archive-download-ux.md 단위 B, 단언 B7 + banner↔list 무모순).
//
// The committed archiveUX.test.tsx gate deferred B7 (polling-window timing) as a
// shell SKIP because it needs fake timers. This file makes the load-bearing
// neutral-transition fix executable-guarded, not inspection-only:
//   (a) an archive stuck non-terminal for 5min flips 처리 중 → 확인 필요 and
//       surfaces a 새로고침 control;
//   (b) 새로고침 restarts exactly ONE bounded polling window (no interval
//       stacking) — and re-arming the request never stacks intervals either;
//   (c) an out-of-band terminal observation (independent minute-level
//       fetchArchives) self-heals the neutral "확인할 수 없습니다" banner without
//       a manual 새로고침.

const STREAM = "cam1";
const NEW_ID = "new1";
const POLL_WINDOW_MS = 5 * 60 * 1000; // bounded polling window (단위B 핵심로직 L101)
const POLL_TICK_MS = 3000; // poll cadence
const WINDOW_REFRESH_MS = 60 * 1000; // independent minute-level fetchArchives cadence

function archiveOf(over: Record<string, unknown>) {
  return {
    id: NEW_ID,
    incidentId: "i1",
    streamKey: STREAM,
    from: "2026-07-12T00:00:00Z",
    to: "2026-07-12T00:05:00Z",
    createdAt: "2026-07-12T00:00:00Z",
    sizeBytes: 1048576,
    filePath: "/archives/new1.mp4",
    status: "processing",
    ...over,
  };
}

// Mutable archive list so a test can flip a row to a terminal state mid-run and
// observe the out-of-band self-heal. The POST create always returns id=NEW_ID.
let currentArchives: Record<string, unknown>[] = [];

function makeFetchMock() {
  return vi.fn((url: string, opts?: RequestInit) => {
    const u = String(url);
    if (u.includes("/api/archives") && opts?.method === "POST") {
      return Promise.resolve({
        ok: true,
        status: 200,
        json: () => Promise.resolve({ archives: [{ id: NEW_ID }] }),
      } as Response);
    }
    if (u.includes("/api/archives")) {
      return Promise.resolve({
        ok: true,
        status: 200,
        json: () => Promise.resolve(currentArchives),
      } as Response);
    }
    if (u.includes("/api/recordings/")) {
      return Promise.resolve({
        ok: true,
        status: 200,
        json: () => Promise.resolve({ timeRanges: [] }),
      } as Response);
    }
    if (u.includes("/api/incidents")) {
      return Promise.resolve({
        ok: true,
        status: 200,
        json: () => Promise.resolve({ data: [] }),
      } as Response);
    }
    return Promise.resolve({ ok: true, status: 200, json: () => Promise.resolve({}) } as Response);
  });
}

let fetchMock: ReturnType<typeof makeFetchMock>;

// Flush the chained fetch → json → setState microtasks under fake timers.
async function flush() {
  for (let i = 0; i < 6; i++) {
    // eslint-disable-next-line no-await-in-loop
    await act(async () => {
      await Promise.resolve();
    });
  }
}

async function advance(ms: number) {
  await act(async () => {
    await vi.advanceTimersByTimeAsync(ms);
  });
}

// Count the poll/refresh GET hits on /api/archives (excludes POST create).
function archiveGetCount(): number {
  return fetchMock.mock.calls.filter(
    ([u, o]) => String(u).includes("/api/archives") && o?.method !== "POST",
  ).length;
}

async function renderAndArchive() {
  render(<RecordingTimeline streamKey={STREAM} onPlaybackRequest={vi.fn()} isPlaying={false} />);
  await flush();
  const archiveBtn = screen.getByRole("button", { name: /^보관$/ });
  fireEvent.click(archiveBtn);
  await flush();
}

beforeEach(() => {
  vi.useFakeTimers();
  localStorage.setItem("token", "test-token");
  currentArchives = [archiveOf({ status: "processing" })];
  fetchMock = makeFetchMock();
  vi.stubGlobal("fetch", fetchMock);
});

afterEach(() => {
  vi.unstubAllGlobals();
  vi.useRealTimers();
  localStorage.clear();
});

describe("archive-download-ux 단위 B — 중립 전이(B7) 회귀", () => {
  // (a) — a non-terminal archive stuck for the full window flips 처리 중 →
  // 확인 필요 and surfaces a 새로고침 control (never a stranded spinner).
  it("(a) stuck-non-terminal 5min → 처리 중 flips to 확인 필요 + 새로고침 control", async () => {
    await renderAndArchive();

    // The request auto-expands the list; the row shows in-progress "처리 중".
    expect(screen.getByText(/처리 중/)).toBeInTheDocument();
    expect(screen.queryByText(/^확인 필요$/)).toBeNull();

    // Hold non-terminal for the entire bounded window.
    await advance(POLL_WINDOW_MS);

    // The row must NOT stay stuck: it transitions to the neutral 확인 필요 label
    // with a manual re-poll control, and the banner mirrors it.
    expect(screen.queryByText(/처리 중/)).toBeNull();
    const label = screen.getByText(/^확인 필요$/);
    const row = label.closest(".rec-timeline-archive-item") as HTMLElement;
    expect(within(row).getByRole("button", { name: /새로고침/ })).toBeInTheDocument();
    expect(screen.getByText(/상태를 확인할 수 없습니다/)).toBeInTheDocument();
  });

  // (b) — 새로고침 restarts exactly ONE bounded polling window, and re-arming
  // the request never stacks intervals (startPolling stops the prior window).
  it("(b) 새로고침 restarts exactly ONE bounded window (no interval stacking)", async () => {
    render(<RecordingTimeline streamKey={STREAM} onPlaybackRequest={vi.fn()} isPlaying={false} />);
    await flush();

    // Arm window A, then re-arm (window B) before A expires. If startPolling
    // failed to stop A, two intervals would both tick.
    const archiveBtn = screen.getByRole("button", { name: /^보관$/ });
    fireEvent.click(archiveBtn);
    await flush();
    fireEvent.click(archiveBtn);
    await flush();

    fetchMock.mockClear();
    await advance(POLL_TICK_MS);
    // A single live interval → exactly one poll GET this tick (stacking → 2+).
    expect(archiveGetCount()).toBe(1);

    // The single window is still bounded: hold non-terminal to expiry → stale.
    await advance(POLL_WINDOW_MS - POLL_TICK_MS);
    const label = screen.getByText(/^확인 필요$/);
    const row = label.closest(".rec-timeline-archive-item") as HTMLElement;
    const refreshBtn = within(row).getByRole("button", { name: /새로고침/ });

    // Click 새로고침 → restarts. The stale marker clears (back to 처리 중).
    fireEvent.click(refreshBtn);
    await flush();
    expect(screen.queryByText(/^확인 필요$/)).toBeNull();
    expect(screen.getByText(/처리 중/)).toBeInTheDocument();

    // The restarted window is again exactly one interval...
    fetchMock.mockClear();
    await advance(POLL_TICK_MS);
    expect(archiveGetCount()).toBe(1);

    // ...and bounded — it terminates in the neutral state exactly once more.
    await advance(POLL_WINDOW_MS - POLL_TICK_MS);
    expect(screen.getAllByText(/^확인 필요$/)).toHaveLength(1);
  });

  // (c) — an out-of-band terminal observation (the independent minute-level
  // fetchArchives, not the stopped poll) self-heals the neutral banner without
  // a manual 새로고침, so banner and list never contradict (준비됨 vs 확인 불가).
  it("(c) out-of-band terminal observation self-heals the neutral banner", async () => {
    await renderAndArchive();

    // Reach the neutral state (poll window elapsed while non-terminal).
    await advance(POLL_WINDOW_MS);
    expect(screen.getByText(/상태를 확인할 수 없습니다/)).toBeInTheDocument();
    expect(screen.getByText(/^확인 필요$/)).toBeInTheDocument();

    // The archive independently reaches a terminal state on the server.
    currentArchives = [archiveOf({ status: "completed", completedAt: "2026-07-12T03:30:00Z" })];

    // The minute-level fetchArchives (window refresh) observes it terminal.
    await advance(WINDOW_REFRESH_MS);

    // Banner self-heals (no manual 새로고침) and the row surfaces as 준비됨 with
    // an active download — the two surfaces agree.
    expect(screen.queryByText(/상태를 확인할 수 없습니다/)).toBeNull();
    expect(screen.queryByText(/^확인 필요$/)).toBeNull();
    const ready = screen.getByText(/^준비됨$/);
    const row = ready.closest(".rec-timeline-archive-item") as HTMLElement;
    expect(within(row).getByRole("button", { name: /다운로드/ })).toBeEnabled();
  });
});
