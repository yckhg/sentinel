import { useEffect, useRef, useState } from "react";
import { fetchWithTimeout, isTimeoutError, timeoutMessage } from "../utils/fetchWithTimeout";
import { formatKstDateTimeSec } from "../utils/datetime";
import Modal from "./Modal";

// -----------------------------------------------------------------------------
// 시스템 상태 패널 — 현재-상태 요약 창 (spec system-status-aggregate).
// 집계 응답(GET /api/health/summary)을 소비한다: 요약 카운트 + 고정 서비스 목록 +
// 예외(이상/오프라인) 장비만. 정상 장비는 행으로 나열되지 않고 카운트로만 대표된다.
// 카메라 연결 상태는 집계 밖 — GET /api/cameras에서 별도 관측한다(계약 3).
// -----------------------------------------------------------------------------

interface SummaryCounts {
  healthy: number;
  abnormal: number;
  offline: number;
}

interface SummaryService {
  id: string;
  status: "healthy" | "unhealthy";
}

interface SummaryException {
  id: string;
  displayName: string;
  category: string;
  ageSec: number;
  reason: string;
}

interface HealthSummary {
  summary: SummaryCounts;
  services: SummaryService[];
  exceptions: SummaryException[];
  exceptionsOverflow: number;
}

interface HealthEvent {
  id: number;
  entityKind: string;
  entityId: string;
  status: string;
  detectedAt: string;
  detail: string;
}

interface CameraStatus {
  id: number;
  name: string;
  status: string;
}

interface DeviceResult {
  id: number;
  siteId: string;
  deviceId: string;
  alias: string;
  lastSeen: string;
  alertState: string;
}

const POLL_INTERVAL_MS = 15_000;

function getAuthHeaders(): HeadersInit {
  const token = localStorage.getItem("token");
  return token
    ? { Authorization: `Bearer ${token}`, "Content-Type": "application/json" }
    : { "Content-Type": "application/json" };
}

function formatDate(s: string | null | undefined): string {
  return formatKstDateTimeSec(s);
}

function formatAge(sec: number): string {
  if (sec < 60) return `${sec}초`;
  if (sec < 3600) return `${Math.floor(sec / 60)}분`;
  if (sec < 86400) return `${Math.floor(sec / 3600)}시간`;
  return `${Math.floor(sec / 86400)}일`;
}

const EMPTY_SUMMARY: HealthSummary = {
  summary: { healthy: 0, abnormal: 0, offline: 0 },
  services: [],
  exceptions: [],
  exceptionsOverflow: 0,
};

export default function HealthPanel() {
  const [data, setData] = useState<HealthSummary>(EMPTY_SUMMARY);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [cameras, setCameras] = useState<CameraStatus[]>([]);
  const firstLoadRef = useRef(true);

  // Device search (assertion D) — current-state lookup, not a listing.
  const [searchInput, setSearchInput] = useState("");
  const [searchResult, setSearchResult] = useState<DeviceResult | null>(null);
  const [searchError, setSearchError] = useState<string | null>(null);
  const [searchLoading, setSearchLoading] = useState(false);

  // History drilldown (assertion E) — a single device's online/offline transitions.
  const [historyOpen, setHistoryOpen] = useState(false);
  const [historyLabel, setHistoryLabel] = useState("");
  const [historyLoading, setHistoryLoading] = useState(false);
  const [historyError, setHistoryError] = useState<string | null>(null);
  const [history, setHistory] = useState<HealthEvent[]>([]);

  const fetchSummary = async () => {
    try {
      const res = await fetchWithTimeout("/api/health/summary", { headers: getAuthHeaders() });
      if (!res.ok) throw new Error(`HTTP ${res.status}`);
      const body: HealthSummary = await res.json();
      setData({
        summary: body.summary ?? EMPTY_SUMMARY.summary,
        services: Array.isArray(body.services) ? body.services : [],
        exceptions: Array.isArray(body.exceptions) ? body.exceptions : [],
        exceptionsOverflow: body.exceptionsOverflow ?? 0,
      });
      setError(null);
    } catch (err) {
      setError(
        isTimeoutError(err)
          ? timeoutMessage()
          : err instanceof Error
            ? err.message
            : "시스템 상태를 불러올 수 없습니다",
      );
    } finally {
      if (firstLoadRef.current) {
        setLoading(false);
        firstLoadRef.current = false;
      }
    }
  };

  const fetchCameras = async () => {
    try {
      const res = await fetchWithTimeout("/api/cameras", { headers: getAuthHeaders() });
      if (!res.ok) return;
      const body: CameraStatus[] = await res.json();
      setCameras(Array.isArray(body) ? body : []);
    } catch {
      // camera status is non-critical and outside the aggregate — ignore failures.
    }
  };

  useEffect(() => {
    fetchSummary();
    fetchCameras();
    const tick = setInterval(() => {
      fetchSummary();
      fetchCameras();
    }, POLL_INTERVAL_MS);
    return () => clearInterval(tick);
  }, []);

  // Search resolves the operator's synthetic siteId:deviceId to the numeric id via
  // GET /api/devices?siteId=&deviceId= (계약 6 델타), then reads the device object.
  const doSearch = async () => {
    const raw = searchInput.trim();
    if (!raw) return;
    setSearchLoading(true);
    setSearchError(null);
    setSearchResult(null);
    const idx = raw.indexOf(":");
    const siteId = idx >= 0 ? raw.slice(0, idx) : "";
    const deviceId = idx >= 0 ? raw.slice(idx + 1) : raw;
    try {
      const url =
        siteId !== ""
          ? `/api/devices?siteId=${encodeURIComponent(siteId)}&deviceId=${encodeURIComponent(deviceId)}`
          : `/api/devices?deviceId=${encodeURIComponent(deviceId)}`;
      const res = await fetchWithTimeout(url, { headers: getAuthHeaders() });
      if (!res.ok) throw new Error(`HTTP ${res.status}`);
      const body = await res.json();
      const device: DeviceResult | undefined = Array.isArray(body) ? body[0] : body;
      if (!device || device.id == null) {
        setSearchError("해당 장비를 찾을 수 없습니다");
        return;
      }
      setSearchResult(device);
    } catch (err) {
      setSearchError(
        isTimeoutError(err) ? timeoutMessage() : err instanceof Error ? err.message : "장비 조회 실패",
      );
    } finally {
      setSearchLoading(false);
    }
  };

  const openHistory = async (entityId: string, label: string) => {
    setHistoryOpen(true);
    setHistoryLabel(label);
    setHistoryLoading(true);
    setHistoryError(null);
    setHistory([]);
    try {
      const res = await fetchWithTimeout(
        `/api/health/events?entity_id=${encodeURIComponent(entityId)}&limit=20`,
        { headers: getAuthHeaders() },
      );
      if (!res.ok) throw new Error(`HTTP ${res.status}`);
      const body: HealthEvent[] = await res.json();
      setHistory(Array.isArray(body) ? body : []);
    } catch (err) {
      setHistoryError(
        isTimeoutError(err) ? timeoutMessage() : err instanceof Error ? err.message : "이력을 불러올 수 없습니다",
      );
    } finally {
      setHistoryLoading(false);
    }
  };

  const { summary, services, exceptions, exceptionsOverflow } = data;

  return (
    <>
      <div className="mgmt-header">
        <h2>시스템 상태</h2>
        <div style={{ marginLeft: "auto", display: "flex", gap: "0.5rem", alignItems: "center" }}>
          <span className="mgmt-card-badge status-badge--ok">정상 {summary.healthy}</span>
          <span
            className={`mgmt-card-badge ${summary.abnormal > 0 ? "status-badge--danger" : "status-badge--ok"}`}
          >
            이상 {summary.abnormal}
          </span>
          <span
            className={`mgmt-card-badge ${summary.offline > 0 ? "status-badge--danger" : "status-badge--ok"}`}
          >
            오프라인 {summary.offline}
          </span>
        </div>
      </div>

      {loading ? (
        <p className="mgmt-loading">로딩 중...</p>
      ) : error ? (
        <p className="mgmt-error">{error}</p>
      ) : (
        <>
          {/* Fixed service set — always complete (assertion F). */}
          <h3 className="mgmt-sub-header">서비스</h3>
          <div className="mgmt-list">
            {services.map((svc) => {
              const healthy = svc.status === "healthy";
              return (
                <div key={svc.id} className="mgmt-card">
                  <div className="mgmt-card-info">
                    <span className="mgmt-card-name">
                      {svc.id}{"  "}
                      <span
                        className={`mgmt-card-badge ${healthy ? "status-badge--ok" : "status-badge--danger"}`}
                      >
                        {healthy ? "정상" : "이상"}
                      </span>
                    </span>
                  </div>
                </div>
              );
            })}
          </div>

          {/* Device search — current state of a single device (assertion D). */}
          <h3 className="mgmt-sub-header">장비 검색</h3>
          <div className="mgmt-form" style={{ display: "flex", gap: "0.5rem", alignItems: "center" }}>
            <input
              type="text"
              value={searchInput}
              onChange={(e) => setSearchInput(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === "Enter") doSearch();
              }}
              placeholder="장비 검색 (예: site:deviceId)"
              aria-label="장비 검색"
            />
            <button className="mgmt-btn mgmt-btn-primary" onClick={doSearch} disabled={searchLoading}>
              {searchLoading ? "조회 중..." : "검색"}
            </button>
          </div>
          {searchError && <p className="mgmt-error">{searchError}</p>}
          {searchResult && (
            <div className="mgmt-card">
              <div className="mgmt-card-info">
                <span className="mgmt-card-name">
                  {searchResult.siteId}:{searchResult.deviceId}
                  {"  "}
                  <span
                    className={`mgmt-card-badge ${searchResult.alertState === "active" ? "status-badge--danger" : "status-badge--ok"}`}
                  >
                    {searchResult.alertState === "active" ? "이상" : "정상"}
                  </span>
                </span>
                <span className="mgmt-card-phone">
                  {searchResult.alias ? `${searchResult.alias} · ` : ""}최근 관측 {formatDate(searchResult.lastSeen)}
                </span>
              </div>
            </div>
          )}

          {/* Exceptions only — abnormal/offline devices. Healthy devices never appear. */}
          <h3 className="mgmt-sub-header">예외 장비 (이상/오프라인)</h3>
          {exceptions.length === 0 ? (
            <p className="mgmt-empty">예외 장비가 없습니다 (모든 장비 정상)</p>
          ) : (
            <div className="mgmt-list">
              {exceptions.map((ex) => {
                const offline = ex.category === "offline";
                return (
                  <button
                    key={ex.id}
                    type="button"
                    className="mgmt-card"
                    style={{ textAlign: "left", cursor: "pointer", width: "100%", border: "none" }}
                    onClick={() => openHistory(ex.id, ex.displayName)}
                    title="상태 전이 이력 보기"
                  >
                    <div className="mgmt-card-info">
                      <span className="mgmt-card-name">
                        <span>{ex.displayName}</span>
                        {"  "}
                        <span className="mgmt-card-badge status-badge--danger">
                          {offline ? "오프라인" : "이상"}
                        </span>
                      </span>
                      <span className="mgmt-card-phone">
                        {ex.reason} · {formatAge(ex.ageSec)} 경과
                      </span>
                    </div>
                  </button>
                );
              })}
            </div>
          )}
          {exceptionsOverflow > 0 && (
            <p className="mgmt-form-hint">
              예외 장비가 상한을 초과했습니다 · 외 {exceptionsOverflow}건 더 있음
            </p>
          )}

          {/* Camera connection status — observed separately (assertion G, 계약 3). */}
          {cameras.length > 0 && (
            <>
              <h3 className="mgmt-sub-header">카메라 연결</h3>
              <div className="mgmt-list">
                {cameras.map((cam) => {
                  const connected = cam.status === "connected";
                  return (
                    <div key={cam.id} className="mgmt-card">
                      <div className="mgmt-card-info">
                        <span className="mgmt-card-name">
                          {cam.name}
                          {"  "}
                          <span
                            className={`mgmt-card-badge ${connected ? "status-badge--ok" : "status-badge--danger"}`}
                          >
                            {connected ? "연결됨" : "연결 끊김"}
                          </span>
                        </span>
                      </div>
                    </div>
                  );
                })}
              </div>
            </>
          )}
        </>
      )}

      {historyOpen && (
        <Modal
          onClose={() => setHistoryOpen(false)}
          ariaLabel="장비 상태 전이 이력"
          style={{ maxWidth: "560px", width: "92vw", maxHeight: "80vh", overflow: "auto" }}
        >
          <h3 style={{ marginTop: 0 }}>{historyLabel} 상태 전이 이력</h3>
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
                      {ev.entityId}{" "}
                      <span
                        className={`mgmt-card-badge ${ev.status === "healthy" ? "status-badge--ok" : "status-badge--danger"}`}
                      >
                        {ev.status === "healthy" ? "복구(online)" : "오프라인(offline)"}
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
            <button className="mgmt-btn mgmt-btn-secondary" onClick={() => setHistoryOpen(false)}>
              닫기
            </button>
          </div>
        </Modal>
      )}
    </>
  );
}
