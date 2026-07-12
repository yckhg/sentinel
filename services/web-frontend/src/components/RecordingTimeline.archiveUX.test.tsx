import { render, screen, fireEvent, waitFor, within } from "@testing-library/react";
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import RecordingTimeline from "./RecordingTimeline";

// Gates for docs/spec/archive-download-ux.md — 단위 B (소비자 측).
// B1 다운로드 게이팅(completed에만) · B2 completed 활성 다운로드 · B3 "준비됨" 전이 ·
// B4 failed 표시(lastError) + 다운로드 미제공 · B5 요청 시 목록 자동 펼침 ·
// B6 미지 status → 미완료 fallback · B8 completedAt 로컬표시.
//
// B7(폴링 윈도우 timing) 은 needs-browser + 라이브 스택 → shell SKIP 게이트로 이관.

// Each archive row is rendered by the component after it fetches /api/archives
// and filters by streamKey. Tests inject a fixed archive list via the fetch mock.
function mockFetch(archives: Record<string, unknown>[]) {
  return vi.fn((url: string, opts?: RequestInit) => {
    if (typeof url === "string" && url.includes("/api/archives") && (!opts || opts.method !== "POST")) {
      return Promise.resolve({ ok: true, status: 200, json: () => Promise.resolve(archives) } as Response);
    }
    if (typeof url === "string" && url.includes("/api/archives") && opts?.method === "POST") {
      return Promise.resolve({ ok: true, status: 200, json: () => Promise.resolve({ archives: [{ id: "new1" }] }) } as Response);
    }
    if (typeof url === "string" && url.includes("/api/recordings/")) {
      return Promise.resolve({ ok: true, status: 200, json: () => Promise.resolve({ timeRanges: [] }) } as Response);
    }
    if (typeof url === "string" && url.includes("/api/incidents")) {
      return Promise.resolve({ ok: true, status: 200, json: () => Promise.resolve({ data: [] }) } as Response);
    }
    return Promise.resolve({ ok: true, status: 200, json: () => Promise.resolve({}) } as Response);
  });
}

function baseArchive(over: Record<string, unknown>) {
  return {
    id: "a1",
    incidentId: "i1",
    streamKey: "cam1",
    from: "2026-07-12T00:00:00Z",
    to: "2026-07-12T00:05:00Z",
    createdAt: "2026-07-12T00:00:00Z",
    sizeBytes: 1048576,
    filePath: "/archives/a1.mp4",
    status: "processing",
    ...over,
  };
}

async function renderAndExpandList(archives: Record<string, unknown>[]) {
  vi.stubGlobal("fetch", mockFetch(archives));
  render(<RecordingTimeline streamKey="cam1" onPlaybackRequest={vi.fn()} isPlaying={false} />);
  // Wait for the archive list toggle to appear, then expand it.
  const toggle = await screen.findByRole("button", { name: /보관 목록/ });
  fireEvent.click(toggle);
  return toggle;
}

beforeEach(() => {
  localStorage.setItem("token", "test-token");
});

afterEach(() => {
  vi.unstubAllGlobals();
  localStorage.clear();
});

describe("archive-download-ux 단위 B — 다운로드 UX", () => {
  // B1 — completed 아닌 행은 활성 다운로드 컨트롤을 노출하지 않는다.
  it("B1: non-completed rows expose no active download control", async () => {
    await renderAndExpandList([
      baseArchive({ id: "p1", status: "processing" }),
      baseArchive({ id: "pr", status: "protecting" }),
    ]);
    await screen.findByText(/처리 중/);
    expect(screen.queryByRole("button", { name: /다운로드/ })).toBeNull();
  });

  // B2 — completed 행은 활성 다운로드 액션을 노출하고 클릭 시 다운로드를 개시한다.
  it("B2: completed row exposes an enabled download action that fetches media", async () => {
    await renderAndExpandList([baseArchive({ id: "c1", status: "completed" })]);
    const btn = await screen.findByRole("button", { name: /다운로드/ });
    expect(btn).toBeEnabled();
    // clicking issues a download request (blob path); mock fetch resolves.
    fireEvent.click(btn);
    await waitFor(() =>
      expect((globalThis.fetch as ReturnType<typeof vi.fn>).mock.calls.some(
        (c) => String(c[0]).includes("/api/archives/c1/download"),
      )).toBe(true),
    );
  });

  // B3 — completed 로 전이하면 요청 피드백이 "준비됨/완료"류 상태로 전이한다.
  // 그 전(미완료)에는 "요청됨/처리 중"류 진행 상태를 보인다.
  it("B3: completed row surfaces a ready/completed affordance, in-progress shows progress", async () => {
    // in-progress → 진행 상태 텍스트
    const { unmount } = { unmount: () => {} };
    await renderAndExpandList([baseArchive({ id: "p", status: "processing" })]);
    expect(await screen.findByText(/처리 중/)).toBeInTheDocument();
    unmount();
    vi.unstubAllGlobals();

    // completed → 진행 라벨이 사라지고 "준비됨/완료" 상태(활성 다운로드)로 전이.
    await renderAndExpandList([baseArchive({ id: "c", status: "completed" })]);
    const btn = await screen.findByRole("button", { name: /다운로드/ });
    expect(btn).toBeEnabled();
    expect(screen.queryByText(/처리 중/)).toBeNull();
  });

  // B4 — failed 행은 실패 사유(lastError 문자열)를 노출하고 다운로드를 제공하지 않는다.
  it("B4: failed row surfaces lastError reason and offers no download", async () => {
    await renderAndExpandList([
      baseArchive({ id: "f1", status: "failed", lastError: "ffmpeg merge: exit status 1" }),
    ]);
    expect(await screen.findByText(/ffmpeg merge: exit status 1/)).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /다운로드/ })).toBeNull();
  });

  // B5 — 보관 요청을 하면 아카이브 목록이 자동으로 가시화(펼침)된다.
  it("B5: submitting an archive request auto-expands the archive list", async () => {
    vi.stubGlobal("fetch", mockFetch([baseArchive({ id: "seed", status: "processing" })]));
    render(<RecordingTimeline streamKey="cam1" onPlaybackRequest={vi.fn()} isPlaying={false} />);

    const archiveBtn = await screen.findByRole("button", { name: /^보관$/ });
    fireEvent.click(archiveBtn);

    // After the request, the list becomes visible without the user manually
    // toggling it — a row (its status label) is observable.
    expect(await screen.findByText(/처리 중/)).toBeInTheDocument();
  });

  // B6 — 미지의 status 값 → 미완료로 취급(다운로드 미제공, "준비됨/완료" 미표시).
  it("B6: unknown status is treated as in-progress (no download, not shown ready)", async () => {
    await renderAndExpandList([baseArchive({ id: "u1", status: "quantum-weird" })]);
    // no active download control
    expect(screen.queryByRole("button", { name: /다운로드/ })).toBeNull();
    // a completed-only affordance (size text used as the ready label) must be absent
    expect(screen.queryByText(/1\.0.?MB/i)).toBeNull();
  });

  // B8 — completed 행은 준비 시각(completedAt, UTC RFC3339)을 로컬 시각으로 변환해 표시한다.
  it("B8: completed row displays completedAt converted to local time", async () => {
    // 2026-07-12T03:30:00Z → KST(+9) = 12:30
    await renderAndExpandList([
      baseArchive({ id: "c8", status: "completed", completedAt: "2026-07-12T03:30:00Z" }),
    ]);
    const btn = await screen.findByRole("button", { name: /다운로드/ });
    const row = btn.closest(".rec-timeline-archive-item") as HTMLElement;
    // The row surfaces the local (KST) ready time, not the raw UTC string.
    expect(within(row).getByText(/12:30/)).toBeInTheDocument();
    expect(within(row).queryByText(/03:30/)).toBeNull();
  });
});
