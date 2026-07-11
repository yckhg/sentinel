import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import RecordingTimeline from "./RecordingTimeline";

function mockJson(url: string) {
  if (url.includes("/api/recordings/")) return { timeRanges: [] };
  if (url.includes("/api/archives")) return [];
  if (url.includes("/api/incidents")) return { data: [] };
  return {};
}

beforeEach(() => {
  vi.stubGlobal(
    "fetch",
    vi.fn((url: string) =>
      Promise.resolve({
        ok: true,
        status: 200,
        json: () => Promise.resolve(mockJson(url)),
      } as Response),
    ),
  );
});

afterEach(() => {
  vi.unstubAllGlobals();
});

describe("RecordingTimeline (#91 slider handles, #101 refresh)", () => {
  it("renders start/end handles as keyboard sliders and a refresh control", async () => {
    render(
      <RecordingTimeline streamKey="cam1" onPlaybackRequest={vi.fn()} isPlaying={false} />,
    );

    const sliders = await screen.findAllByRole("slider");
    expect(sliders).toHaveLength(2);
    sliders.forEach((s) => {
      expect(s).toHaveAttribute("tabindex", "0");
      expect(s).toHaveAttribute("aria-valuenow");
    });

    // #101 — a manual refresh control exists.
    expect(
      screen.getByRole("button", { name: "타임라인 새로고침" }),
    ).toBeInTheDocument();
  });

  it("moves the start handle with arrow keys", async () => {
    render(
      <RecordingTimeline streamKey="cam1" onPlaybackRequest={vi.fn()} isPlaying={false} />,
    );
    const start = (await screen.findAllByRole("slider"))[0]!;
    const before = Number(start.getAttribute("aria-valuenow"));
    fireEvent.keyDown(start, { key: "ArrowRight" });
    await waitFor(() => {
      const after = Number(
        screen.getAllByRole("slider")[0]!.getAttribute("aria-valuenow"),
      );
      expect(after).toBeGreaterThan(before);
    });
  });
});
