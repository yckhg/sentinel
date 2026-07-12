/**
 * Merge an incoming crisis alert into the current banner list.
 *
 * Single reducer for BOTH add paths (live `crisis_alert` push AND
 * `/api/incidents/active` reconnect backfill):
 *  - drop it if its incidentId was already dismissed this session
 *    (no resurrection on WS re-send/backfill — #97 dismissed-memory),
 *  - drop it if a banner with the same incidentId is already showing (dedup),
 *  - otherwise prepend it.
 *
 * Keyed strictly on `incidentId` — the same key CrisisAlertBanner uses for its
 * React keys, dedup and dismiss (①'s model). Payloads without an incidentId
 * never reach here (the banner's `toAlert()` drops them upstream).
 */
export function reduceCrisisAlerts<T extends { incidentId: string }>(
  prev: T[],
  incoming: T,
  dismissed: ReadonlySet<string>,
): T[] {
  if (dismissed.has(incoming.incidentId)) return prev;
  if (prev.some((a) => a.incidentId === incoming.incidentId)) return prev;
  return [incoming, ...prev];
}
