import { useState, useCallback } from "react";
import useWebSocket from "../hooks/useWebSocket";

interface CrisisAlert {
  id: string;
  description: string;
  occurredAt: string;
  siteId: string;
}

export default function CrisisAlertBanner() {
  const [alerts, setAlerts] = useState<CrisisAlert[]>([]);

  const handleMessage = useCallback(
    (msg: { type: string; payload: Record<string, unknown>; timestamp: string }) => {
      if (msg.type === "crisis_alert") {
        const p = msg.payload;
        const alert: CrisisAlert = {
          id: `${p.incidentId ?? Date.now()}`,
          description: (p.description as string) || "위기 상황 발생",
          occurredAt: (p.occurredAt as string) || msg.timestamp,
          siteId: (p.siteId as string) || "",
        };
        setAlerts((prev) => [alert, ...prev]);
      }
    },
    []
  );

  useWebSocket(handleMessage);

  const dismiss = (id: string) => {
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
                {new Date(alert.occurredAt).toLocaleTimeString("ko-KR")}
              </span>
              <span className="crisis-banner-desc">{alert.description}</span>
            </div>
          </div>
          <button
            className="crisis-banner-close"
            onClick={() => dismiss(alert.id)}
            aria-label="닫기"
          >
            ✕
          </button>
        </div>
      ))}
    </div>
  );
}
