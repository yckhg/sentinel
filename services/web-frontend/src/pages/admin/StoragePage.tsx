import { useEffect, useState } from "react";
import { fetchWithTimeout, isTimeoutError, timeoutMessage } from "../../utils/fetchWithTimeout";
import { formatKstDateTime } from "../../utils/datetime";
import Modal from "../../components/Modal";
import { navigate } from "../../utils/navigation";

// Relocated from ManagementPage "저장소 관리" section (admin-IA leaf: /admin/storage).
// Behavior-preserving move — same endpoints, same request/response contract.
// Local copies of the module-level helpers that ManagementPage kept private
// (getAuthHeaders / formatBytes / errorMessage) so this leaf is self-contained
// without editing shared _helpers.

interface StorageStats {
  recordingsBytes: number;
  archivesBytes: number;
  totalUsedBytes: number;
  archiveCount: number;
  diskTotalBytes?: number;
  diskUsedBytes?: number;
  diskAvailableBytes?: number;
}

interface Archive {
  id: string;
  incidentId: string;
  streamKey: string;
  from: string;
  to: string;
  createdAt: string;
  sizeBytes: number;
  status: string;
  error?: string;
}

function getAuthHeaders(): HeadersInit {
  const token = localStorage.getItem("token");
  return token
    ? { Authorization: `Bearer ${token}`, "Content-Type": "application/json" }
    : { "Content-Type": "application/json" };
}

function formatBytes(bytes: number): string {
  if (bytes === 0) return "0 B";
  const units = ["B", "KB", "MB", "GB", "TB"];
  const i = Math.floor(Math.log(bytes) / Math.log(1024));
  const val = bytes / Math.pow(1024, i);
  return `${val < 10 ? val.toFixed(1) : Math.round(val)} ${units[i]}`;
}

const errorMessage = (err: unknown): string =>
  isTimeoutError(err)
    ? timeoutMessage()
    : err instanceof Error
      ? err.message
      : "요청을 처리하지 못했습니다";

export default function StoragePage() {
  const [storageStats, setStorageStats] = useState<StorageStats | null>(null);
  const [storageLoading, setStorageLoading] = useState(true);
  const [storageError, setStorageError] = useState<string | null>(null);
  const [archives, setArchives] = useState<Archive[]>([]);
  const [archivesLoading, setArchivesLoading] = useState(true);
  // Archive-fetch failure now surfaces an error (#103 오류처리 균질화) instead of
  // being silently swallowed and mistaken for an empty state — symmetric with
  // the storage-usage error state above.
  const [archivesError, setArchivesError] = useState<string | null>(null);
  const [archiveDeleteTarget, setArchiveDeleteTarget] = useState<Archive | null>(null);
  const [archiveDeleteLoading, setArchiveDeleteLoading] = useState(false);
  const [incidentDeleteTarget, setIncidentDeleteTarget] = useState<string | null>(null);
  const [incidentDeleteLoading, setIncidentDeleteLoading] = useState(false);
  const [archiveDownloading, setArchiveDownloading] = useState<string | null>(null);
  // Shared inline error for the confirm/delete modals — only one is open at a
  // time. Delete failures keep the modal open and surface the failure (#103).
  const [actionError, setActionError] = useState<string | null>(null);

  const fetchStorage = async () => {
    try {
      const res = await fetchWithTimeout("/api/storage", { headers: getAuthHeaders() });
      if (!res.ok) throw new Error(`HTTP ${res.status}`);
      const data: StorageStats = await res.json();
      setStorageStats(data);
      setStorageError(null);
    } catch (err) {
      setStorageError(
        isTimeoutError(err) ? timeoutMessage() : err instanceof Error ? err.message : "저장소 정보를 불러올 수 없습니다"
      );
    } finally {
      setStorageLoading(false);
    }
  };

  const fetchArchives = async () => {
    try {
      const res = await fetchWithTimeout("/api/archives", { headers: getAuthHeaders() });
      if (!res.ok) throw new Error(`HTTP ${res.status}`);
      const data: Archive[] = await res.json();
      setArchives(data || []);
      setArchivesError(null);
    } catch (err) {
      // Surface the failure (#103) — do not silently fall through to empty state.
      setArchivesError(
        isTimeoutError(err) ? timeoutMessage() : err instanceof Error ? err.message : "보관 영상을 불러올 수 없습니다"
      );
    } finally {
      setArchivesLoading(false);
    }
  };

  const handleArchiveDelete = async () => {
    if (!archiveDeleteTarget) return;
    setArchiveDeleteLoading(true);
    setActionError(null);
    try {
      const res = await fetchWithTimeout(`/api/archives/${archiveDeleteTarget.id}`, {
        method: "DELETE",
        headers: getAuthHeaders(),
      });
      if (!res.ok && res.status !== 204) throw new Error(`HTTP ${res.status}`);
      setArchiveDeleteTarget(null);
      await Promise.all([fetchArchives(), fetchStorage()]);
    } catch (err) {
      setActionError(errorMessage(err)); // keep modal open, surface failure (#103)
    } finally {
      setArchiveDeleteLoading(false);
    }
  };

  const handleIncidentArchiveDelete = async () => {
    if (!incidentDeleteTarget) return;
    setIncidentDeleteLoading(true);
    setActionError(null);
    try {
      const res = await fetchWithTimeout(`/api/archives/incident/${encodeURIComponent(incidentDeleteTarget)}`, {
        method: "DELETE",
        headers: getAuthHeaders(),
      });
      if (!res.ok && res.status !== 204) throw new Error(`HTTP ${res.status}`);
      setIncidentDeleteTarget(null);
      await Promise.all([fetchArchives(), fetchStorage()]);
    } catch (err) {
      setActionError(errorMessage(err));
    } finally {
      setIncidentDeleteLoading(false);
    }
  };

  const handleArchiveDownload = async (archiveId: string) => {
    setArchiveDownloading(archiveId);
    try {
      const res = await fetchWithTimeout(`/api/archives/${archiveId}/download`, {
        headers: { Authorization: `Bearer ${localStorage.getItem("token") || ""}` },
      });
      if (!res.ok) {
        const data = await res.json().catch(() => ({}));
        alert(data.error || `다운로드 실패 (${res.status})`);
        return;
      }
      const blob = await res.blob();
      const url = URL.createObjectURL(blob);
      const a = document.createElement("a");
      a.href = url;
      a.download = `${archiveId}.mp4`;
      document.body.appendChild(a);
      a.click();
      document.body.removeChild(a);
      URL.revokeObjectURL(url);
    } catch {
      alert("다운로드 서비스에 연결할 수 없습니다");
    } finally {
      setArchiveDownloading(null);
    }
  };

  useEffect(() => {
    fetchStorage();
    fetchArchives();
  }, []);

  return (
    <div className="admin-page" data-testid="admin-page" data-slug="storage">
      <button
        type="button"
        className="admin-back"
        data-testid="admin-back"
        onClick={() => navigate("/admin")}
      >
        ← 관리
      </button>
      <h1 className="admin-page-title">저장소 관리</h1>

      {/* Storage usage */}
      {storageLoading ? (
        <p className="mgmt-loading">로딩 중...</p>
      ) : storageError ? (
        <p className="mgmt-error">{storageError}</p>
      ) : storageStats && (
        <div className="mgmt-storage-section">
          {/* Disk usage bar */}
          {storageStats.diskTotalBytes != null && storageStats.diskTotalBytes > 0 && (() => {
            const pct = Math.round((storageStats.diskUsedBytes! / storageStats.diskTotalBytes!) * 100);
            const isWarning = pct >= 80;
            return (
              <div className="mgmt-storage-disk">
                <div className="mgmt-storage-disk-header">
                  <span>디스크 사용량</span>
                  <span className={isWarning ? "mgmt-storage-warning" : ""}>
                    {pct}%{isWarning ? " (경고)" : ""}
                  </span>
                </div>
                <div className="mgmt-storage-bar">
                  <div
                    className={`mgmt-storage-bar-fill${isWarning ? " mgmt-storage-bar-warning" : ""}`}
                    style={{ width: `${Math.min(pct, 100)}%` }}
                  />
                </div>
                <div className="mgmt-storage-disk-detail">
                  <span>전체: {formatBytes(storageStats.diskTotalBytes!)}</span>
                  <span>사용: {formatBytes(storageStats.diskUsedBytes!)}</span>
                  <span>가용: {formatBytes(storageStats.diskAvailableBytes!)}</span>
                </div>
              </div>
            );
          })()}

          {/* Recording/Archive breakdown */}
          <div className="mgmt-storage-breakdown">
            <div className="mgmt-storage-item">
              <span className="mgmt-storage-label">녹화 데이터</span>
              <span className="mgmt-storage-value">{formatBytes(storageStats.recordingsBytes)}</span>
            </div>
            <div className="mgmt-storage-item">
              <span className="mgmt-storage-label">보관 영상</span>
              <span className="mgmt-storage-value">{formatBytes(storageStats.archivesBytes)}</span>
            </div>
            <div className="mgmt-storage-item">
              <span className="mgmt-storage-label">합계</span>
              <span className="mgmt-storage-value">{formatBytes(storageStats.totalUsedBytes)}</span>
            </div>
          </div>
        </div>
      )}

      {/* Archive list — grouped by incident */}
      <h3 className="mgmt-sub-header">보관 영상 목록</h3>
      {archivesLoading ? (
        <p className="mgmt-loading">로딩 중...</p>
      ) : archivesError ? (
        <p className="mgmt-error">{archivesError}</p>
      ) : archives.length === 0 ? (
        <p className="mgmt-empty">보관된 영상이 없습니다</p>
      ) : (() => {
        // Group archives by incidentId
        const grouped = archives.reduce<Record<string, Archive[]>>((acc, a) => {
          const key = a.incidentId || "unknown";
          if (!acc[key]) acc[key] = [];
          acc[key].push(a);
          return acc;
        }, {});
        const incidentIds = Object.keys(grouped).sort((a, b) => {
          // Sort by earliest createdAt descending
          const aTime = (grouped[a] ?? [])[0]?.createdAt ?? "";
          const bTime = (grouped[b] ?? [])[0]?.createdAt ?? "";
          return bTime.localeCompare(aTime);
        });
        return (
          <div className="mgmt-list">
            {incidentIds.map((incidentId) => {
              const group = grouped[incidentId] ?? [];
              const totalSize = group.reduce((sum, a) => sum + (a.sizeBytes || 0), 0);
              const allCompleted = group.every((a) => a.status === "completed");
              const anyProcessing = group.some((a) => a.status === "processing" || a.status === "pending");
              const firstArchive = group[0];
              return (
                <div key={incidentId} className="mgmt-card mgmt-card-incident-group">
                  <div className="mgmt-card-info">
                    <span className="mgmt-card-name">
                      {incidentId}
                      <span className={`mgmt-badge-archive mgmt-badge-archive-${allCompleted ? "completed" : anyProcessing ? "processing" : "failed"}`}>
                        {allCompleted ? "완료" : anyProcessing ? "처리중" : "실패"}
                      </span>
                      <span className="mgmt-badge-archive-count">{group.length}개 카메라</span>
                    </span>
                    {firstArchive && (
                      <span className="mgmt-card-phone">
                        {formatKstDateTime(firstArchive.from)} ~ {formatKstDateTime(firstArchive.to)}
                      </span>
                    )}
                    <span className="mgmt-card-phone">
                      합계: {totalSize > 0 ? formatBytes(totalSize) : "-"}
                    </span>
                  </div>
                  <div className="mgmt-card-actions">
                    <button
                      className="mgmt-btn mgmt-btn-small mgmt-btn-danger"
                      onClick={() => setIncidentDeleteTarget(incidentId)}
                    >
                      전체 삭제
                    </button>
                  </div>
                  {/* Individual camera archives within the group */}
                  <div className="mgmt-incident-archives">
                    {group.map((archive) => (
                      <div key={archive.id} className="mgmt-incident-archive-item">
                        <span className="mgmt-incident-archive-key">{archive.streamKey}</span>
                        <span className={`mgmt-badge-archive mgmt-badge-archive-${archive.status}`}>
                          {archive.status === "completed"
                            ? formatBytes(archive.sizeBytes)
                            : archive.status === "processing"
                              ? "처리중"
                              : archive.status === "pending"
                                ? "대기"
                                : "실패"}
                        </span>
                        {archive.status === "completed" && (
                          <button
                            className="mgmt-btn mgmt-btn-small"
                            onClick={() => handleArchiveDownload(archive.id)}
                            disabled={archiveDownloading === archive.id}
                          >
                            {archiveDownloading === archive.id ? "..." : "다운로드"}
                          </button>
                        )}
                        {archive.status === "failed" && archive.error && (
                          <span className="mgmt-badge-archive mgmt-badge-archive-failed" title={archive.error}>
                            {archive.error.length > 30 ? archive.error.substring(0, 30) + "..." : archive.error}
                          </span>
                        )}
                        <button
                          className="mgmt-btn mgmt-btn-small mgmt-btn-danger"
                          onClick={() => setArchiveDeleteTarget(archive)}
                        >
                          삭제
                        </button>
                      </div>
                    ))}
                  </div>
                </div>
              );
            })}
          </div>
        );
      })()}

      {/* Archive delete confirmation dialog */}
      {archiveDeleteTarget && (
        <Modal
          onClose={() => { setArchiveDeleteTarget(null); setActionError(null); }}
          ariaLabel="보관 영상 삭제 확인"
        >
          <p className="mgmt-modal-text">
            <strong>{archiveDeleteTarget.streamKey}</strong> 보관 영상을 삭제하시겠습니까?<br />
            <small>{archiveDeleteTarget.sizeBytes > 0 ? formatBytes(archiveDeleteTarget.sizeBytes) : ""} 디스크 공간이 확보됩니다.</small>
          </p>
          {actionError && <p className="mgmt-form-error">{actionError}</p>}
          <div className="mgmt-form-actions">
            <button
              className="mgmt-btn mgmt-btn-danger"
              onClick={handleArchiveDelete}
              disabled={archiveDeleteLoading}
            >
              {archiveDeleteLoading ? "삭제 중..." : "삭제"}
            </button>
            <button
              className="mgmt-btn mgmt-btn-secondary"
              onClick={() => { setArchiveDeleteTarget(null); setActionError(null); }}
            >
              취소
            </button>
          </div>
        </Modal>
      )}

      {/* Incident archive delete confirmation dialog */}
      {incidentDeleteTarget && (
        <Modal
          onClose={() => { setIncidentDeleteTarget(null); setActionError(null); }}
          ariaLabel="사건 보관 영상 삭제 확인"
        >
          <p className="mgmt-modal-text">
            <strong>{incidentDeleteTarget}</strong> 사건의 모든 보관 영상을 삭제하시겠습니까?<br />
            <small>해당 사건의 모든 카메라 영상이 삭제됩니다.</small>
          </p>
          {actionError && <p className="mgmt-form-error">{actionError}</p>}
          <div className="mgmt-form-actions">
            <button
              className="mgmt-btn mgmt-btn-danger"
              onClick={handleIncidentArchiveDelete}
              disabled={incidentDeleteLoading}
            >
              {incidentDeleteLoading ? "삭제 중..." : "전체 삭제"}
            </button>
            <button
              className="mgmt-btn mgmt-btn-secondary"
              onClick={() => { setIncidentDeleteTarget(null); setActionError(null); }}
            >
              취소
            </button>
          </div>
        </Modal>
      )}
    </div>
  );
}
