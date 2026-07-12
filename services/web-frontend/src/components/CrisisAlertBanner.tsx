import { useCallback, useState } from "react";
import useWebSocket, { type WsState } from "../hooks/useWebSocket";
import { fetchWithTimeout } from "../utils/fetchWithTimeout";
import { formatKstTime } from "../utils/datetime";
import EmergencyCallButton from "./EmergencyCallButton";

interface SiteInfo {
  address?: string;
  managerName?: string;
  managerPhone?: string;
}

interface CrisisAlert {
  incidentId: string;
  description: string;
  occurredAt: string;
  siteId: string;
  site?: SiteInfo;
}

const WS_STATE_TEXT: Record<WsState, string> = {
  connected: "실시간 연결됨",
  disconnected: "연결 끊김",
  reconnecting: "재접속 중...",
};

/**
 * Map a `crisis_alert`-isomorphic payload (live push OR /api/incidents/active
 * backfill item) into a banner. Dedup/removal key is strictly `incidentId`
 * (spec §출력 2, 단언 C·P). Payloads without an `incidentId` are ignored.
 */
function toAlert(p: Record<string, unknown>): CrisisAlert | null {
  const rawId = p.incidentId;
  if (rawId === undefined || rawId === null || rawId === "") return null;
  const site = (p.site as SiteInfo | undefined) ?? undefined;
  return {
    incidentId: String(rawId),
    description: (p.description as string) || "위기 상황 발생",
    occurredAt: (p.occurredAt as string) || "",
    siteId: (p.siteId as string) || "",
    site,
  };
}

export default function CrisisAlertBanner() {
  const [alerts, setAlerts] = useState<CrisisAlert[]>([]);

  const handleMessage = useCallback(
    (msg: { type: string; payload: Record<string, unknown>; timestamp: string }) => {
      if (msg.type !== "crisis_alert") return;
      const alert = toAlert(msg.payload);
      if (!alert) return; // malformed / no incidentId
      if (!alert.occurredAt) alert.occurredAt = msg.timestamp;
      setAlerts((prev) => {
        // dedup strictly by incidentId (no Date.now fallback)
        if (prev.some((a) => a.incidentId === alert.incidentId)) return prev;
        return [alert, ...prev];
      });
    },
    []
  );

  // Reconnect backfill (best-effort, decoupled from ws-state marker).
  // TRUE sync keyed by incidentId: add missing unresolved, dedup, remove stale.
  // On failure (5xx / timeout / network) keep the existing set as-is.
  const handleReconnect = useCallback(() => {
    const token = localStorage.getItem("token");
    if (!token) return;
    fetchWithTimeout("/api/incidents/active", {
      headers: { Authorization: `Bearer ${token}` },
    })
      .then((res) => (res.ok ? res.json() : Promise.reject(new Error(`HTTP ${res.status}`))))
      .then((items: Record<string, unknown>[]) => {
        if (!Array.isArray(items)) return;
        const backfilled = items
          .map((it) => toAlert(it))
          .filter((a): a is CrisisAlert => a !== null);
        const activeIds = new Set(backfilled.map((a) => a.incidentId));
        setAlerts((prev) => {
          // (3) remove stale: banners whose incidentId no longer unresolved.
          const kept = prev.filter((a) => activeIds.has(a.incidentId));
          const keptIds = new Set(kept.map((a) => a.incidentId));
          // (1)/(2) add missing without duplicating already-shown banners.
          const additions = backfilled.filter((a) => !keptIds.has(a.incidentId));
          return [...additions, ...kept];
        });
      })
      .catch(() => {
        // best-effort: keep existing banner set untouched, retry on next reconnect.
      });
  }, []);

  const { wsState } = useWebSocket(handleMessage, handleReconnect);

  const dismiss = (incidentId: string) => {
    setAlerts((prev) => prev.filter((a) => a.incidentId !== incidentId));
  };

  return (
    <div className="crisis-banner-container">
      <div
        className={`ws-status-indicator ws-status-${wsState}`}
        data-ws-state={wsState}
        role="status"
        aria-live="polite"
      >
        <span className="ws-status-dot" />
        <span className="ws-status-text">{WS_STATE_TEXT[wsState]}</span>
      </div>

      {alerts.map((alert) => (
        <div key={alert.incidentId} className="crisis-banner" data-incident-id={alert.incidentId}>
          <div className="crisis-banner-content">
            <span className="crisis-banner-icon">🚨</span>
            <div className="crisis-banner-text">
              <span className="crisis-banner-time">
                {formatKstTime(alert.occurredAt, true)}
              </span>
              <span className="crisis-banner-desc">{alert.description}</span>
            </div>
          </div>
          <div className="crisis-banner-actions">
            <EmergencyCallButton compact siteAddress={alert.site?.address} />
            <button
              className="crisis-banner-close"
              onClick={() => dismiss(alert.incidentId)}
              aria-label="닫기"
            >
              ✕
            </button>
          </div>
        </div>
      ))}
    </div>
  );
}
