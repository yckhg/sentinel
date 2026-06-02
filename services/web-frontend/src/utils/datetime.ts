// Server stores all timestamps in UTC.
// - SQLite datetime(): "YYYY-MM-DD HH:MM:SS" (space-separated, no timezone marker)
// - ISO strings:        "YYYY-MM-DDTHH:MM:SSZ" (or with offset)
// Naive strings (no Z / no offset) MUST be interpreted as UTC, then displayed in KST.

const KST = "Asia/Seoul";

/** Parse a server timestamp, treating timezone-less strings as UTC. Returns null if invalid. */
export function parseServerDate(s: string | null | undefined): Date | null {
  if (!s) return null;
  const v = s.trim();
  const hasTz = /[zZ]$|[+-]\d{2}:?\d{2}$/.test(v);
  const norm = hasTz ? v : v.replace(" ", "T") + "Z";
  const t = Date.parse(norm);
  return Number.isNaN(t) ? null : new Date(t);
}

/** Milliseconds since epoch for a server timestamp (UTC-aware). 0 if invalid. */
export function parseServerTimeMs(s: string | null | undefined): number {
  const d = parseServerDate(s);
  return d ? d.getTime() : 0;
}

function kstParts(d: Date): Record<string, string> {
  const fmt = new Intl.DateTimeFormat("en-CA", {
    timeZone: KST,
    year: "numeric",
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
    second: "2-digit",
    hour12: false,
  });
  const o: Record<string, string> = {};
  for (const p of fmt.formatToParts(d)) o[p.type] = p.value;
  if (o.hour === "24") o.hour = "00"; // some engines emit "24" at midnight
  return o;
}

/** "YYYY-MM-DD HH:MM" in KST. */
export function formatKstDateTime(s: string | null | undefined): string {
  const d = parseServerDate(s);
  if (!d) return "-";
  const o = kstParts(d);
  return `${o.year}-${o.month}-${o.day} ${o.hour}:${o.minute}`;
}

/** "YYYY-MM-DD HH:MM:SS" in KST. */
export function formatKstDateTimeSec(s: string | null | undefined): string {
  const d = parseServerDate(s);
  if (!d) return "-";
  const o = kstParts(d);
  return `${o.year}-${o.month}-${o.day} ${o.hour}:${o.minute}:${o.second}`;
}

/** "YYYY-MM-DD" in KST. */
export function formatKstDate(s: string | null | undefined): string {
  const d = parseServerDate(s);
  if (!d) return "-";
  const o = kstParts(d);
  return `${o.year}-${o.month}-${o.day}`;
}

/** "HH:MM" (or "HH:MM:SS" when withSeconds) in KST. */
export function formatKstTime(s: string | null | undefined, withSeconds = false): string {
  const d = parseServerDate(s);
  if (!d) return "-";
  const o = kstParts(d);
  return withSeconds ? `${o.hour}:${o.minute}:${o.second}` : `${o.hour}:${o.minute}`;
}

/** Format a Date object (already absolute) as "HH:MM" / "HH:MM:SS" in KST. */
export function formatKstClock(d: Date, withSeconds = false): string {
  if (Number.isNaN(d.getTime())) return "-";
  const o = kstParts(d);
  return withSeconds ? `${o.hour}:${o.minute}:${o.second}` : `${o.hour}:${o.minute}`;
}
