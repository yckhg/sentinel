import { useEffect, useRef, useState } from "react";
import { fetchWithTimeout, isTimeoutError, timeoutMessage } from "../utils/fetchWithTimeout";

interface HealthEntry {
  kind: "service" | "sensor";
  id: string;
  name: string;
  status: "healthy" | "unhealthy";
  lastCheck: string;
  since: string;
  detail: string;
  source: string;
}

interface HealthEvent {
  id: number;
  entityKind: string;
  entityId: string;
  status: string;
  detectedAt: string;
  detail: string;
}

const POLL_INTERVAL_MS = 15_000;

function getAuthHeaders(): HeadersInit {
  const token = localStorage.getItem("token");
  return token
    ? { Authorization: `Bearer ${token}`, "Content-Type": "application/json" }
    : { "Content-Type": "application/json" };
}

function parseServerTimeMs(s: string | null | undefined): number {
  if (!s) return 0;
  if (s.includes("T")) {
    const t = Date.parse(s);
    return Number.isNaN(t) ? 0 : t;
  }
  const iso = s.replace(" ", "T") + "Z";
  const t = Date.parse(iso);
  return Number.isNaN(t) ? 0 : t;
}

function formatDate(s: string | null | undefined): string {
  const t = parseServerTimeMs(s);
  if (!t) return "-";
  const d = new Date(t);
  const pad = (n: number) => n.toString().padStart(2, "0");
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${pad(d.getHours())}:${pad(d.getMinutes())}:${pad(d.getSeconds())}`;
}

function formatSince(s: string | null | undefined, nowMs: number): string {
  const t = parseServerTimeMs(s);
  if (!t) return "-";
  const diff = Math.max(0, Math.floor((nowMs - t) / 1000));
  if (diff < 60) return `${diff}초`;
  if (diff < 3600) return `${Math.floor(diff / 60)}분`;
  if (diff < 86400) return `${Math.floor(diff / 3600)}시간`;
  return `${Math.floor(diff / 86400)}일`;
}

export default function HealthPanel() {
  const [entries, setEntries] = useState<HealthEntry[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [nowMs, setNowMs] = useState(Date.now());

  const [historyOpen, setHistoryOpen] = useState(false);
  const [historyLoading, setHistoryLoading] = useState(false);
  const [historyError, setHistoryError] = useState<string | null>(null);
  const [history, setHistory] = useState<HealthEvent[]>([]);
  const firstLoadRef = useRef(true);

  const fetchHealth = async () => {
    try {
      const res = await fetchWithTimeout("/api/health", { headers: getAuthHeaders() });
      if (!res.ok) throw new Error(`HTTP ${res.status}`);
      const data: HealthEntry[] = await res.json();
      setEntries(Array.isArray(data) ? data : []);
      setError(null);
    } catch (err) {
      setError(
        isTimeoutError(err)
          ? timeoutMessage()
          : err instanceof Error
            ? err.message
            : "Health 상태를 불러올 수 없습니다",
      );
    } finally {
      if (firstLoadRef.current) {
        setLoading(false);
        firstLoadRef.current = false;
      }
    }
  };

  useEffect(() => {
    fetchHealth();
    const tick = setInterval(() => {
      fetchHealth();
      setNowMs(Date.now());
    }, POLL_INTERVAL_MS);
    const fastTick = setInterval(() => setNowMs(Date.now()), 5_000);
    return () => {
      clearInterval(tick);
      clearInterval(fastTick);
    };
  }, []);

  const openHistory = async () => {
    setHistoryOpen(true);
    setHistoryLoading(true);
    setHistoryError(null);
    try {
      const res = await fetchWithTimeout("/api/health/events?limit=20", {
        headers: getAuthHeaders(),
      });
      if (!res.ok) throw new Error(`HTTP ${res.status}`);
      const data: HealthEvent[] = await res.json();
      setHistory(Array.isArray(data) ? data : []);
    } catch (err) {
      setHistoryError(
        isTimeoutError(err)
          ? timeoutMessage()
          : err instanceof Error
            ? err.message
            : "이력을 불러올 수 없습니다",
      );
    } finally {
      setHistoryLoading(false);
    }
  };

  const unhealthyCount = entries.filter((e) => e.status === "unhealthy").length;
  const healthyCount = entries.length - unhealthyCount;

  return (
    <>
      <div className="mgmt-header">
        <h2>시스템 상태</h2>
        <div style={{ marginLeft: "auto", display: "flex", gap: "0.5rem", alignItems: "center" }}>
          <span
            className="mgmt-card-badge"
            style={{
              background: unhealthyCount > 0 ? "#c62828" : "#2e7d32",
              color: "#fff",
            }}
          >
            이상 {unhealthyCount} / 정상 {healthyCount}
          </span>
          <button className="mgmt-btn mgmt-btn-small" onClick={openHistory}>
            이력
          </button>
        </div>
      </div>

      {loading ? (
        <p className="mgmt-loading">로딩 중...</p>
      ) : error ? (
        <p className="mgmt-error">{error}</p>
      ) : entries.length === 0 ? (
        <p className="mgmt-empty">모니터 대상이 없습니다</p>
      ) : (
        <div className="mgmt-list">
          {entries.map((e) => {
            const healthy = e.status === "healthy";
            return (
              <div key={`${e.kind}|${e.id}`} className="mgmt-card">
                <div className="mgmt-card-info">
                  <span className="mgmt-card-name">
                    <span style={{ opacity: 0.7, marginRight: "0.4rem" }}>
                      {e.kind === "service" ? "[서비스]" : "[센서]"}
                    </span>
                    {e.name}
                    {"  "}
                    <span
                      className="mgmt-card-badge"
                      style={{
                        background: healthy ? "#2e7d32" : "#c62828",
                        color: "#fff",
                      }}
                    >
                      {healthy ? "정상" : "이상"}
                    </span>
                  </span>
                  <span className="mgmt-card-phone">
                    {healthy ? "정상 유지" : "이상 발생"} · {formatSince(e.since, nowMs)} 경과
                  </span>
                  <span className="mgmt-card-email">
                    최근 점검 {formatDate(e.lastCheck)}
                    {e.detail ? ` · ${e.detail}` : ""}
                  </span>
                </div>
              </div>
            );
          })}
        </div>
      )}

      {historyOpen && (
        <div className="mgmt-modal-overlay" onClick={() => setHistoryOpen(false)}>
          <div
            className="mgmt-modal"
            onClick={(ev) => ev.stopPropagation()}
            style={{ maxWidth: "560px", width: "92vw", maxHeight: "80vh", overflow: "auto" }}
          >
            <h3 style={{ marginTop: 0 }}>최근 Health 이력 (20건)</h3>
            {historyLoading ? (
              <p className="mgmt-loading">로딩 중...</p>
            ) : historyError ? (
              <p className="mgmt-error">{historyError}</p>
            ) : history.length === 0 ? (
              <p className="mgmt-empty">기록된 이력이 없습니다</p>
            ) : (
              <div className="mgmt-list">
                {history.map((ev) => (
                  <div key={ev.id} className="mgmt-card">
                    <div className="mgmt-card-info">
                      <span className="mgmt-card-name">
                        <span style={{ opacity: 0.7, marginRight: "0.4rem" }}>
                          {ev.entityKind === "service" ? "[서비스]" : "[센서]"}
                        </span>
                        {ev.entityId}{" "}
                        <span
                          className="mgmt-card-badge"
                          style={{
                            background: ev.status === "healthy" ? "#2e7d32" : "#c62828",
                            color: "#fff",
                          }}
                        >
                          {ev.status === "healthy" ? "복구" : "이상"}
                        </span>
                      </span>
                      <span className="mgmt-card-phone">{formatDate(ev.detectedAt)}</span>
                      {ev.detail && <span className="mgmt-card-email">{ev.detail}</span>}
                    </div>
                  </div>
                ))}
              </div>
            )}
            <div className="mgmt-form-actions" style={{ marginTop: "1rem" }}>
              <button
                className="mgmt-btn mgmt-btn-secondary"
                onClick={() => setHistoryOpen(false)}
              >
                닫기
              </button>
            </div>
          </div>
        </div>
      )}
    </>
  );
}
