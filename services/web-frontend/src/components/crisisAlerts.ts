export interface CrisisAlert {
  id: string;
  description: string;
  occurredAt: string;
  siteId: string;
}

/**
 * Build a stable, unique id for a crisis alert.
 *
 * When the payload carries an incidentId we key on it so the same incident
 * re-sent over a WebSocket reconnect dedupes to one banner. When it doesn't,
 * we fall back to a monotonic local sequence (never Date.now(), which collides
 * when two alerts arrive in the same millisecond and breaks React keys).
 */
export function crisisAlertId(
  payload: Record<string, unknown>,
  fallbackSeq: number,
): string {
  const inc = payload.incidentId;
  if (inc !== undefined && inc !== null && String(inc) !== "") {
    return `incident:${String(inc)}`;
  }
  return `local:${fallbackSeq}`;
}

/**
 * Merge an incoming alert into the current list:
 *  - drop it if the id was already dismissed (don't resurrect on re-send),
 *  - drop it if an alert with the same id is already showing (dedup),
 *  - otherwise prepend it.
 */
export function reduceCrisisAlerts(
  prev: CrisisAlert[],
  incoming: CrisisAlert,
  dismissed: ReadonlySet<string>,
): CrisisAlert[] {
  if (dismissed.has(incoming.id)) return prev;
  if (prev.some((a) => a.id === incoming.id)) return prev;
  return [incoming, ...prev];
}
