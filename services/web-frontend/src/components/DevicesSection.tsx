import { useEffect, useState } from "react";
import { fetchWithTimeout, isTimeoutError, timeoutMessage } from "../utils/fetchWithTimeout";

interface Device {
  id: number;
  siteId: string;
  deviceId: string;
  alias: string;
  firstSeen: string;
  lastSeen: string;
  deletedAt: string | null;
}

const POLL_INTERVAL_MS = 10_000;
const ALIVE_THRESHOLD_MS = 30_000;

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

function isAlive(lastSeen: string, nowMs: number): boolean {
  const t = parseServerTimeMs(lastSeen);
  if (!t) return false;
  return nowMs - t < ALIVE_THRESHOLD_MS;
}

export default function DevicesSection() {
  const [devices, setDevices] = useState<Device[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [nowMs, setNowMs] = useState(Date.now());

  const [editId, setEditId] = useState<number | null>(null);
  const [editAlias, setEditAlias] = useState("");
  const [editLoading, setEditLoading] = useState(false);
  const [editError, setEditError] = useState<string | null>(null);

  const [deleteTarget, setDeleteTarget] = useState<Device | null>(null);
  const [deleteLoading, setDeleteLoading] = useState(false);

  const fetchDevices = async () => {
    try {
      const res = await fetchWithTimeout("/api/devices", { headers: getAuthHeaders() });
      if (!res.ok) throw new Error(`HTTP ${res.status}`);
      const data: Device[] = await res.json();
      setDevices(Array.isArray(data) ? data : []);
      setError(null);
    } catch (err) {
      setError(
        isTimeoutError(err)
          ? timeoutMessage()
          : err instanceof Error
            ? err.message
            : "장비 목록을 불러올 수 없습니다"
      );
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    fetchDevices();
    const tick = setInterval(() => {
      fetchDevices();
      setNowMs(Date.now());
    }, POLL_INTERVAL_MS);
    const fastTick = setInterval(() => setNowMs(Date.now()), 5_000);
    return () => {
      clearInterval(tick);
      clearInterval(fastTick);
    };
  }, []);

  const startEdit = (d: Device) => {
    setEditId(d.id);
    setEditAlias(d.alias || "");
    setEditError(null);
  };

  const cancelEdit = () => {
    setEditId(null);
    setEditError(null);
  };

  const handleEditSave = async () => {
    if (editId === null) return;
    setEditLoading(true);
    setEditError(null);
    try {
      const res = await fetchWithTimeout(`/api/devices/${editId}`, {
        method: "PATCH",
        headers: getAuthHeaders(),
        body: JSON.stringify({ alias: editAlias }),
      });
      if (!res.ok) {
        const body = await res.json().catch(() => null);
        throw new Error(body?.error || `HTTP ${res.status}`);
      }
      setEditId(null);
      await fetchDevices();
    } catch (err) {
      setEditError(
        isTimeoutError(err)
          ? timeoutMessage()
          : err instanceof Error
            ? err.message
            : "별칭 저장 실패"
      );
    } finally {
      setEditLoading(false);
    }
  };

  const handleDelete = async () => {
    if (!deleteTarget) return;
    setDeleteLoading(true);
    try {
      const res = await fetchWithTimeout(`/api/devices/${deleteTarget.id}`, {
        method: "DELETE",
        headers: getAuthHeaders(),
      });
      if (!res.ok && res.status !== 204) {
        const body = await res.json().catch(() => null);
        throw new Error(body?.error || `HTTP ${res.status}`);
      }
      setDeleteTarget(null);
      await fetchDevices();
    } catch (err) {
      alert(err instanceof Error ? err.message : "삭제 실패");
      setDeleteTarget(null);
    } finally {
      setDeleteLoading(false);
    }
  };

  const sortedDevices = [...devices].sort((a, b) => {
    const aAlive = isAlive(a.lastSeen, nowMs);
    const bAlive = isAlive(b.lastSeen, nowMs);
    if (aAlive !== bAlive) return aAlive ? -1 : 1;
    return parseServerTimeMs(b.lastSeen) - parseServerTimeMs(a.lastSeen);
  });

  return (
    <>
      <div className="mgmt-header">
        <h2>장비(센서) 관리</h2>
      </div>

      {loading ? (
        <p className="mgmt-loading">로딩 중...</p>
      ) : error ? (
        <p className="mgmt-error">{error}</p>
      ) : sortedDevices.length === 0 ? (
        <p className="mgmt-empty">활성 장비가 없습니다</p>
      ) : (
        <div className="mgmt-list">
          {sortedDevices.map((d) => {
            const alive = isAlive(d.lastSeen, nowMs);
            const isEditing = editId === d.id;
            return (
              <div
                key={d.id}
                className={`mgmt-card${isEditing ? " mgmt-card-editing" : ""}`}
              >
                {isEditing ? (
                  <>
                    <div className="mgmt-form-field">
                      <label>별칭</label>
                      <input
                        type="text"
                        value={editAlias}
                        autoFocus
                        placeholder="(비워두면 별칭 제거)"
                        onChange={(e) => setEditAlias(e.target.value)}
                        onKeyDown={(e) => {
                          if (e.key === "Enter") handleEditSave();
                          if (e.key === "Escape") cancelEdit();
                        }}
                      />
                    </div>
                    {editError && <p className="mgmt-form-error">{editError}</p>}
                    <div className="mgmt-form-actions">
                      <button
                        className="mgmt-btn mgmt-btn-primary mgmt-btn-small"
                        onClick={handleEditSave}
                        disabled={editLoading}
                      >
                        {editLoading ? "저장 중..." : "저장"}
                      </button>
                      <button
                        className="mgmt-btn mgmt-btn-secondary mgmt-btn-small"
                        onClick={cancelEdit}
                        disabled={editLoading}
                      >
                        취소
                      </button>
                    </div>
                  </>
                ) : (
                  <>
                    <div className="mgmt-card-info">
                      <span className="mgmt-card-name">
                        {d.alias || d.deviceId}
                        {" "}
                        {alive ? (
                          <span
                            className="mgmt-card-badge"
                            style={{ background: "#2e7d32", color: "#fff" }}
                          >
                            온라인
                          </span>
                        ) : (
                          <span
                            className="mgmt-card-badge"
                            style={{ background: "#c62828", color: "#fff" }}
                          >
                            오프라인
                          </span>
                        )}
                      </span>
                      <span className="mgmt-card-phone">
                        {d.siteId} / {d.deviceId}
                      </span>
                      <span className="mgmt-card-email">
                        최초 {formatDate(d.firstSeen)} · 최근 {formatDate(d.lastSeen)}
                      </span>
                    </div>
                    <div className="mgmt-card-actions">
                      <button
                        className="mgmt-btn mgmt-btn-small"
                        onClick={() => startEdit(d)}
                      >
                        별칭
                      </button>
                      <button
                        className="mgmt-btn mgmt-btn-small mgmt-btn-danger"
                        onClick={() => setDeleteTarget(d)}
                      >
                        삭제
                      </button>
                    </div>
                  </>
                )}
              </div>
            );
          })}
        </div>
      )}

      {deleteTarget && (
        <div className="mgmt-modal-overlay" onClick={() => setDeleteTarget(null)}>
          <div className="mgmt-modal" onClick={(e) => e.stopPropagation()}>
            <p className="mgmt-modal-text">
              <strong>{deleteTarget.alias || deleteTarget.deviceId}</strong> 장비를 삭제하시겠습니까?
              <br />
              <small>
                삭제 후 같은 장비가 다시 신호를 보내면 자동으로 복원됩니다.
              </small>
            </p>
            <div className="mgmt-form-actions">
              <button
                className="mgmt-btn mgmt-btn-danger"
                onClick={handleDelete}
                disabled={deleteLoading}
              >
                {deleteLoading ? "삭제 중..." : "삭제"}
              </button>
              <button
                className="mgmt-btn mgmt-btn-secondary"
                onClick={() => setDeleteTarget(null)}
              >
                취소
              </button>
            </div>
          </div>
        </div>
      )}
    </>
  );
}
