import { useState, useCallback, useRef } from "react";
import useWebSocket from "../hooks/useWebSocket";
import { formatKstTime } from "../utils/datetime";
import EmergencyCallButton from "./EmergencyCallButton";
import { crisisAlertId, reduceCrisisAlerts, type CrisisAlert } from "./crisisAlerts";

export default function CrisisAlertBanner() {
  const [alerts, setAlerts] = useState<CrisisAlert[]>([]);
  // Monotonic sequence for alerts without an incidentId, and the set of ids the
  // user has dismissed so a re-send over WS reconnect doesn't pop them back.
  const seqRef = useRef(0);
  const dismissedRef = useRef<Set<string>>(new Set());

  const handleMessage = useCallback(
    (msg: { type: string; payload: Record<string, unknown>; timestamp: string }) => {
      if (msg.type !== "crisis_alert") return;
      const p = msg.payload;
      const alert: CrisisAlert = {
        id: crisisAlertId(p, ++seqRef.current),
        description: (p.description as string) || "위기 상황 발생",
        occurredAt: (p.occurredAt as string) || msg.timestamp,
        siteId: (p.siteId as string) || "",
      };
      setAlerts((prev) => reduceCrisisAlerts(prev, alert, dismissedRef.current));
    },
    []
  );

  useWebSocket(handleMessage);

  const dismiss = (id: string) => {
    dismissedRef.current.add(id);
    setAlerts((prev) => prev.filter((a) => a.id !== id));
  };

  if (alerts.length === 0) return null;

  return (
    <div className="crisis-banner-container">
      {alerts.map((alert) => (
        <div key={alert.id} className="crisis-banner">
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
            <EmergencyCallButton compact />
            <button
              className="crisis-banner-close"
              onClick={() => dismiss(alert.id)}
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
