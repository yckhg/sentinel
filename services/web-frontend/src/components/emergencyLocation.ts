/** Format GPS coordinates for a 119 report (5 decimal places ~= 1m). */
export function formatGps(lat: number, lon: number): string {
  return `${lat.toFixed(5)}, ${lon.toFixed(5)}`;
}

/**
 * Human-readable GPS line for the 119 dialog. Distinguishes "still locating",
 * "permission denied" and "unavailable" so a denied lookup is not silently
 * swallowed (#98).
 */
export function gpsStatusText(opts: {
  coords: string | null;
  loading: boolean;
  denied: boolean;
}): string {
  if (opts.coords) return `GPS: ${opts.coords}`;
  if (opts.loading) return "위치 확인 중...";
  if (opts.denied) return "위치 권한이 거부되어 GPS를 표시할 수 없습니다";
  return "위치 정보를 사용할 수 없습니다";
}
