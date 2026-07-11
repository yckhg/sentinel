import { render, screen, fireEvent } from "@testing-library/react";
import { describe, it, expect, vi } from "vitest";
import HLSPlayer from "./HLSPlayer";

// status="disconnected" keeps the HLS engine from initialising in jsdom, so the
// test focuses purely on the a11y wrapper markup.
describe("HLSPlayer a11y (#91, #100)", () => {
  it("exposes the camera cell as a keyboard-operable button", () => {
    const onToggle = vi.fn();
    render(
      <HLSPlayer
        url="http://x/stream.m3u8"
        cameraName="정문"
        zone="A구역"
        status="disconnected"
        expanded={false}
        onToggleExpand={onToggle}
      />,
    );
    const cell = screen.getByRole("button", { name: /정문/ });
    expect(cell).toHaveAttribute("tabindex", "0");
    expect(cell).toHaveAttribute("aria-pressed", "false");

    fireEvent.keyDown(cell, { key: "Enter" });
    expect(onToggle).toHaveBeenCalledTimes(1);
    fireEvent.keyDown(cell, { key: " " });
    expect(onToggle).toHaveBeenCalledTimes(2);
  });

  it("labels the video element with the camera name (#100)", () => {
    render(
      <HLSPlayer
        url="http://x/stream.m3u8"
        cameraName="후문"
        zone="B구역"
        status="disconnected"
        expanded={false}
        onToggleExpand={vi.fn()}
      />,
    );
    const video = document.querySelector("video");
    expect(video).not.toBeNull();
    expect(video).toHaveAttribute("aria-label", "후문 영상");
  });
});
