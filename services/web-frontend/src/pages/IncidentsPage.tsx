import { useEffect, useState, useCallback } from "react";
import { fetchWithTimeout, isTimeoutError, timeoutMessage } from "../utils/fetchWithTimeout";

interface Incident {
  id: number;
  siteId: string;
  description: string;
  occurredAt: string;
  confirmedAt: string | null;
  confirmedBy: string | null;
}

interface Pagination {
  page: number;
  limit: number;
  total: number;
}

function getAuthHeaders(): Record<string, string> {
  const token = localStorage.getItem("token");
  return {
    Authorization: `Bearer ${token}`,
    "Content-Type": "application/json",
  };
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

function formatDateInput(date: Date): string {
  const y = date.getFullYear();
  const m = String(date.getMonth() + 1).padStart(2, "0");
  const d = String(date.getDate()).padStart(2, "0");
  return `${y}-${m}-${d}`;
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

  const fetchIncidents = useCallback(async (page: number, from: string, to: string, append: boolean) => {
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
    fetchIncidents(1, "", "", false);
  }, [fetchIncidents]);

  const handleFilter = () => {
    setFilterApplied({ from: dateFrom, to: dateTo });
    fetchIncidents(1, dateFrom, dateTo, false);
  };

  const handleClearFilter = () => {
    setDateFrom("");
    setDateTo("");
    setFilterApplied({ from: "", to: "" });
    fetchIncidents(1, "", "", false);
  };

  const handleLoadMore = () => {
    const nextPage = pagination.page + 1;
    fetchIncidents(nextPage, filterApplied.from, filterApplied.to, true);
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
        <div className="incidents-filter-row">
          <input
            type="date"
            value={dateFrom}
            onChange={(e) => setDateFrom(e.target.value)}
            max={dateTo || formatDateInput(new Date())}
            className="incidents-date-input"
          />
          <span className="incidents-filter-sep">~</span>
          <input
            type="date"
            value={dateTo}
            onChange={(e) => setDateTo(e.target.value)}
            min={dateFrom}
            max={formatDateInput(new Date())}
            className="incidents-date-input"
          />
        </div>
        <div className="incidents-filter-actions">
          <button className="mgmt-btn mgmt-btn-primary" onClick={handleFilter}>
            조회
          </button>
          {(filterApplied.from || filterApplied.to) && (
            <button className="mgmt-btn mgmt-btn-secondary" onClick={handleClearFilter}>
              초기화
            </button>
          )}
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
            <div key={inc.id} className="incidents-card">
              <div className="incidents-card-header">
                <span className="incidents-card-time">{formatDateTime(inc.occurredAt)}</span>
                <span className={`incidents-card-badge ${inc.confirmedAt ? "confirmed" : "unconfirmed"}`}>
                  {inc.confirmedAt ? "확인됨" : "미확인"}
                </span>
              </div>
              <div className="incidents-card-desc">{inc.description || "(설명 없음)"}</div>
              {inc.confirmedAt && (
                <div className="incidents-card-confirm">
                  {inc.confirmedBy && <span>{inc.confirmedBy}</span>}
                  <span>{formatDateTime(inc.confirmedAt)}</span>
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
    </div>
  );
}
