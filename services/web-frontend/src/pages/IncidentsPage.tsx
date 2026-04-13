import { useEffect, useState, useCallback } from "react";
import { fetchWithTimeout, isTimeoutError, timeoutMessage } from "../utils/fetchWithTimeout";
import DualCalendar from "../components/DualCalendar";

interface Incident {
  id: number;
  siteId: string;
  description: string;
  occurredAt: string;
  confirmedAt: string | null;
  confirmedBy: string | null;
  isTest: boolean;
  status: string;
  resolvedAt: string | null;
  resolvedBy: string | null;
  resolutionNotes: string | null;
  resolvedByKind: string | null;
  resolvedById: string | null;
  resolvedByLabel: string | null;
}

function resolverDisplay(inc: Incident): { icon: string; text: string } | null {
  if (inc.status !== "resolved") return null;
  const label = inc.resolvedByLabel || inc.resolvedById || inc.resolvedBy || "";
  switch (inc.resolvedByKind) {
    case "web":
      return { icon: "🖥", text: label ? `웹 해제 — ${label}` : "웹 해제" };
    case "sensor_button":
      return { icon: "🔘", text: label ? `센서 버튼 — ${label}` : "센서 버튼 해제" };
    default:
      return label ? { icon: "👤", text: label } : null;
  }
}

interface Pagination {
  page: number;
  limit: number;
  total: number;
}

type StatusFilter = "" | "open" | "acknowledged" | "resolved";

function getAuthHeaders(): Record<string, string> {
  const token = localStorage.getItem("token");
  return {
    Authorization: `Bearer ${token}`,
    "Content-Type": "application/json",
  };
}

function isAdmin(): boolean {
  const token = localStorage.getItem("token");
  if (!token) return false;
  try {
    const parts = token.split(".");
    if (parts.length < 2) return false;
    const encoded = parts[1];
    if (!encoded) return false;
    const payload = JSON.parse(atob(encoded));
    return payload.role === "admin";
  } catch {
    return false;
  }
}

function formatDateTime(iso: string): string {
  try {
    const d = new Date(iso);
    if (isNaN(d.getTime())) return iso;
    const month = String(d.getMonth() + 1).padStart(2, "0");
    const day = String(d.getDate()).padStart(2, "0");
    const hours = String(d.getHours()).padStart(2, "0");
    const minutes = String(d.getMinutes()).padStart(2, "0");
    return `${d.getFullYear()}-${month}-${day} ${hours}:${minutes}`;
  } catch {
    return iso;
  }
}

function statusLabel(status: string): string {
  switch (status) {
    case "open": return "미확인";
    case "acknowledged": return "확인됨";
    case "resolved": return "조치완료";
    default: return status;
  }
}

function statusBadgeClass(status: string): string {
  switch (status) {
    case "open": return "status-open";
    case "acknowledged": return "status-acknowledged";
    case "resolved": return "status-resolved";
    default: return "";
  }
}

interface ResolveModalProps {
  incident: Incident;
  onClose: () => void;
  onResolved: () => void;
}

function ResolveModal({ incident, onClose, onResolved }: ResolveModalProps) {
  const [notes, setNotes] = useState("");
  const [sending, setSending] = useState(false);
  const [error, setError] = useState("");

  const handleSubmit = async () => {
    if (!notes.trim()) {
      setError("조치 내용을 입력해주세요.");
      return;
    }
    setSending(true);
    setError("");
    try {
      const res = await fetchWithTimeout(`/api/incidents/${incident.id}/resolve`, {
        method: "PATCH",
        headers: getAuthHeaders(),
        body: JSON.stringify({ resolutionNotes: notes.trim() }),
      });
      if (!res.ok) {
        const body = await res.json().catch(() => ({ error: `HTTP ${res.status}` }));
        throw new Error(body.error || `HTTP ${res.status}`);
      }
      onResolved();
    } catch (err) {
      setError(
        isTimeoutError(err)
          ? timeoutMessage()
          : err instanceof Error
          ? err.message
          : "조치 완료 처리에 실패했습니다."
      );
    } finally {
      setSending(false);
    }
  };

  return (
    <div className="mgmt-modal-overlay" onClick={onClose}>
      <div className="mgmt-modal" onClick={(e) => e.stopPropagation()}>
        <p className="mgmt-modal-text">
          <strong>조치 완료 처리</strong>
        </p>
        <p className="mgmt-modal-text" style={{ fontSize: "0.85rem", color: "#666" }}>
          {formatDateTime(incident.occurredAt)} — {incident.description || "(설명 없음)"}
        </p>
        <div className="mgmt-form-field">
          <label>조치 내용 (필수)</label>
          <textarea
            value={notes}
            onChange={(e) => setNotes(e.target.value)}
            placeholder="조치 내용을 입력하세요"
            rows={4}
            autoFocus
            style={{ width: "100%", resize: "vertical" }}
          />
        </div>
        {error && <p style={{ color: "#d32f2f", fontSize: "0.8rem", marginBottom: "8px" }}>{error}</p>}
        <div className="mgmt-form-actions" style={{ justifyContent: "center" }}>
          <button className="mgmt-btn mgmt-btn-secondary" onClick={onClose} disabled={sending}>
            취소
          </button>
          <button className="mgmt-btn mgmt-btn-primary" onClick={handleSubmit} disabled={sending}>
            {sending ? "처리 중..." : "조치 완료"}
          </button>
        </div>
      </div>
    </div>
  );
}

export default function IncidentsPage() {
  const [incidents, setIncidents] = useState<Incident[]>([]);
  const [pagination, setPagination] = useState<Pagination>({ page: 1, limit: 20, total: 0 });
  const [loading, setLoading] = useState(true);
  const [loadingMore, setLoadingMore] = useState(false);
  const [error, setError] = useState("");
  const [dateFrom, setDateFrom] = useState("");
  const [dateTo, setDateTo] = useState("");
  const [filterApplied, setFilterApplied] = useState({ from: "", to: "" });
  const [statusFilter, setStatusFilter] = useState<StatusFilter>("");
  const [resolveTarget, setResolveTarget] = useState<Incident | null>(null);
  const [actionLoading, setActionLoading] = useState<number | null>(null);
  const admin = useState(() => isAdmin())[0];

  const fetchIncidents = useCallback(async (page: number, from: string, to: string, status: StatusFilter, append: boolean) => {
    if (append) {
      setLoadingMore(true);
    } else {
      setLoading(true);
    }
    setError("");

    try {
      const params = new URLSearchParams();
      params.set("page", String(page));
      params.set("limit", "20");
      if (from) params.set("from", from);
      if (to) params.set("to", to + "T23:59:59");
      if (status) params.set("status", status);

      const res = await fetchWithTimeout(`/api/incidents?${params}`, { headers: getAuthHeaders() });
      if (!res.ok) throw new Error(`HTTP ${res.status}`);
      const json = await res.json();

      if (append) {
        setIncidents((prev) => [...prev, ...json.data]);
      } else {
        setIncidents(json.data);
      }
      setPagination(json.pagination);
    } catch (err) {
      setError(isTimeoutError(err) ? timeoutMessage() : "사고 이력을 불러오지 못했습니다.");
      console.error("fetch incidents error:", err);
    } finally {
      setLoading(false);
      setLoadingMore(false);
    }
  }, []);

  useEffect(() => {
    fetchIncidents(1, "", "", "", false);
  }, [fetchIncidents]);

  const handleClearFilter = () => {
    setDateFrom("");
    setDateTo("");
    setFilterApplied({ from: "", to: "" });
    fetchIncidents(1, "", "", statusFilter, false);
  };

  const handleStatusChange = (newStatus: StatusFilter) => {
    setStatusFilter(newStatus);
    fetchIncidents(1, filterApplied.from, filterApplied.to, newStatus, false);
  };

  const handleLoadMore = () => {
    const nextPage = pagination.page + 1;
    fetchIncidents(nextPage, filterApplied.from, filterApplied.to, statusFilter, true);
  };

  const refresh = () => {
    fetchIncidents(1, filterApplied.from, filterApplied.to, statusFilter, false);
  };

  const handleAcknowledge = async (inc: Incident) => {
    setActionLoading(inc.id);
    try {
      const res = await fetchWithTimeout(`/api/incidents/${inc.id}/acknowledge`, {
        method: "PATCH",
        headers: getAuthHeaders(),
      });
      if (!res.ok) {
        const body = await res.json().catch(() => ({ error: `HTTP ${res.status}` }));
        throw new Error(body.error || `HTTP ${res.status}`);
      }
      refresh();
    } catch (err) {
      alert(err instanceof Error ? err.message : "확인 처리에 실패했습니다.");
    } finally {
      setActionLoading(null);
    }
  };

  const totalPages = Math.ceil(pagination.total / pagination.limit);
  const hasMore = pagination.page < totalPages;

  if (loading) {
    return <div className="incidents-loading">불러오는 중...</div>;
  }

  return (
    <div className="page">
      <h2>사고 이력</h2>

      <div className="incidents-filter">
        <DualCalendar
          startDate={dateFrom}
          endDate={dateTo}
          onSelect={(start, end) => {
            setDateFrom(start);
            setDateTo(end);
            setFilterApplied({ from: start, to: end });
            fetchIncidents(1, start, end, statusFilter, false);
          }}
          onReset={handleClearFilter}
        />
        <div className="incidents-status-filter">
          {([
            ["", "전체"],
            ["open", "미확인"],
            ["acknowledged", "확인됨"],
            ["resolved", "조치완료"],
          ] as [StatusFilter, string][]).map(([value, label]) => (
            <button
              key={value}
              className={`incidents-status-btn${statusFilter === value ? " active" : ""}`}
              onClick={() => handleStatusChange(value)}
            >
              {label}
            </button>
          ))}
        </div>
      </div>

      {error && <div className="incidents-error">{error}</div>}

      <div className="incidents-count">
        총 {pagination.total}건
      </div>

      {incidents.length === 0 && !error ? (
        <div className="incidents-empty">사고 이력이 없습니다.</div>
      ) : (
        <div className="incidents-list">
          {incidents.map((inc) => (
            <div key={inc.id} className={`incidents-card${inc.isTest ? " incidents-card-test" : ""}`}>
              <div className="incidents-card-header">
                <span className="incidents-card-time">{formatDateTime(inc.occurredAt)}</span>
                <div className="incidents-card-badges">
                  {inc.isTest && (
                    <span className="incidents-card-badge test-badge">테스트</span>
                  )}
                  <span className={`incidents-card-badge ${statusBadgeClass(inc.status)}`}>
                    {statusLabel(inc.status)}
                  </span>
                </div>
              </div>
              <div className="incidents-card-desc">{inc.description || "(설명 없음)"}</div>
              {inc.confirmedAt && (
                <div className="incidents-card-confirm">
                  {inc.confirmedBy && <span>{inc.confirmedBy}</span>}
                  <span>{formatDateTime(inc.confirmedAt)}</span>
                </div>
              )}
              {inc.status === "resolved" && inc.resolvedAt && (() => {
                const resolver = resolverDisplay(inc);
                return (
                  <div className="incidents-card-resolution">
                    <div className="incidents-card-resolution-header">조치 완료</div>
                    <div className="incidents-card-resolution-notes">{inc.resolutionNotes}</div>
                    <div className="incidents-card-resolution-meta">
                      {resolver ? (
                        <span>{resolver.icon} {resolver.text}</span>
                      ) : (
                        inc.resolvedBy && <span>{inc.resolvedBy}</span>
                      )}
                      <span>{formatDateTime(inc.resolvedAt)}</span>
                    </div>
                  </div>
                );
              })()}
              {admin && inc.status !== "resolved" && (
                <div className="incidents-card-actions">
                  {inc.status === "open" && (
                    <button
                      className="mgmt-btn mgmt-btn-secondary incidents-action-btn"
                      onClick={() => handleAcknowledge(inc)}
                      disabled={actionLoading === inc.id}
                    >
                      {actionLoading === inc.id ? "처리 중..." : "확인"}
                    </button>
                  )}
                  <button
                    className="mgmt-btn mgmt-btn-primary incidents-action-btn"
                    onClick={() => setResolveTarget(inc)}
                    disabled={actionLoading === inc.id}
                  >
                    조치 완료
                  </button>
                </div>
              )}
            </div>
          ))}
        </div>
      )}

      {hasMore && (
        <button
          className="incidents-load-more mgmt-btn mgmt-btn-secondary"
          onClick={handleLoadMore}
          disabled={loadingMore}
        >
          {loadingMore ? "불러오는 중..." : "더 보기"}
        </button>
      )}

      {resolveTarget && (
        <ResolveModal
          incident={resolveTarget}
          onClose={() => setResolveTarget(null)}
          onResolved={() => {
            setResolveTarget(null);
            refresh();
          }}
        />
      )}
    </div>
  );
}
