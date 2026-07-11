import { describe, it, expect } from "vitest";
import { formatGps, gpsStatusText } from "./emergencyLocation";

describe("formatGps (#98)", () => {
  it("formats to 5 decimals", () => {
    expect(formatGps(37.123456, 127.987654)).toBe("37.12346, 127.98765");
  });
});

describe("gpsStatusText (#98)", () => {
  it("shows coordinates when available", () => {
    expect(
      gpsStatusText({ coords: "37.00000, 127.00000", loading: false, denied: false }),
    ).toBe("GPS: 37.00000, 127.00000");
  });

  it("shows a locating state while loading", () => {
    expect(gpsStatusText({ coords: null, loading: true, denied: false })).toBe(
      "위치 확인 중...",
    );
  });

  it("surfaces a denied permission instead of silently hiding it", () => {
    const text = gpsStatusText({ coords: null, loading: false, denied: true });
    expect(text).toMatch(/거부/);
  });

  it("shows an unavailable state otherwise", () => {
    expect(gpsStatusText({ coords: null, loading: false, denied: false })).toMatch(
      /사용할 수 없습니다/,
    );
  });
});
