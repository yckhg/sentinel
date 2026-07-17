import { useEffect, useState } from "react";
import { navigate } from "../../utils/navigation";
import {
  fetchWithTimeout,
  isTimeoutError,
  timeoutMessage,
} from "../../utils/fetchWithTimeout";
import { formatKstDateTime } from "../../utils/datetime";
import Modal from "../../components/Modal";

interface TempLink {
  id: string;
  label: string;
  createdAt: string;
  expiresAt: string;
  url?: string;
}

function getAuthHeaders(): HeadersInit {
  const token = localStorage.getItem("token");
  return token
    ? { Authorization: `Bearer ${token}`, "Content-Type": "application/json" }
    : { "Content-Type": "application/json" };
}

const errorMessage = (err: unknown): string =>
  isTimeoutError(err)
    ? timeoutMessage()
    : err instanceof Error
      ? err.message
      : "요청을 처리하지 못했습니다";

// Relocated from the former ManagementPage "임시 CCTV 링크" section (admin-IA).
// Behavior-preserving move: list / create / revoke (confirm modal, #103 revoke
// failure surfaced) against the unchanged Links API. admin-only, gate inherited
// via the routing/seam contract.
export default function CctvLinksPage() {
  const [tempLinks, setTempLinks] = useState<TempLink[]>([]);
  const [linksLoading, setLinksLoading] = useState(true);
  const [linksError, setLinksError] = useState<string | null>(null);
  const [createLinkLoading, setCreateLinkLoading] = useState(false);
  const [newLinkUrl, setNewLinkUrl] = useState<string | null>(null);
  const [copySuccess, setCopySuccess] = useState(false);
  const [revokeTarget, setRevokeTarget] = useState<TempLink | null>(null);
  const [revokeLoading, setRevokeLoading] = useState(false);
  const [actionError, setActionError] = useState<string | null>(null);

  const fetchTempLinks = async () => {
    try {
      const res = await fetchWithTimeout("/api/links", {
        headers: getAuthHeaders(),
      });
      if (!res.ok) throw new Error(`HTTP ${res.status}`);
      const data: TempLink[] = await res.json();
      setTempLinks(data || []);
      setLinksError(null);
    } catch (err) {
      setLinksError(
        isTimeoutError(err)
          ? timeoutMessage()
          : err instanceof Error
            ? err.message
            : "임시 링크를 불러올 수 없습니다"
      );
    } finally {
      setLinksLoading(false);
    }
  };

  useEffect(() => {
    fetchTempLinks();
  }, []);

  const handleCreateLink = async () => {
    setCreateLinkLoading(true);
    setNewLinkUrl(null);
    try {
      const res = await fetchWithTimeout("/api/links/temp", {
        method: "POST",
        headers: getAuthHeaders(),
        body: JSON.stringify({}),
      });
      if (!res.ok) {
        const body = await res.json().catch(() => null);
        throw new Error(body?.error || `HTTP ${res.status}`);
      }
      const data = await res.json();
      setNewLinkUrl(data.url);
      await fetchTempLinks();
    } catch (err) {
      setLinksError(
        isTimeoutError(err)
          ? timeoutMessage()
          : err instanceof Error
            ? err.message
            : "링크 생성 실패"
      );
    } finally {
      setCreateLinkLoading(false);
    }
  };

  const handleCopyUrl = async (url: string) => {
    try {
      await navigator.clipboard.writeText(url);
      setCopySuccess(true);
      setTimeout(() => setCopySuccess(false), 2000);
    } catch {
      // Fallback for older browsers
      const input = document.createElement("input");
      input.value = url;
      document.body.appendChild(input);
      input.select();
      document.execCommand("copy");
      document.body.removeChild(input);
      setCopySuccess(true);
      setTimeout(() => setCopySuccess(false), 2000);
    }
  };

  const handleRevoke = async () => {
    if (!revokeTarget) return;
    setRevokeLoading(true);
    setActionError(null);
    try {
      const res = await fetchWithTimeout(`/api/links/${revokeTarget.id}`, {
        method: "DELETE",
        headers: getAuthHeaders(),
      });
      if (!res.ok && res.status !== 204) throw new Error(`HTTP ${res.status}`);
      setRevokeTarget(null);
      setNewLinkUrl(null);
      await fetchTempLinks();
    } catch (err) {
      // Keep modal open, surface failure — do not mistake failure for success (#103).
      setActionError(errorMessage(err));
    } finally {
      setRevokeLoading(false);
    }
  };

  return (
    <div className="admin-page" data-testid="admin-page" data-slug="cctv-links">
      <button
        type="button"
        className="admin-back"
        data-testid="admin-back"
        onClick={() => navigate("/admin")}
      >
        ← 관리
      </button>

      <div className="mgmt-header">
        <h1 className="admin-page-title">임시 CCTV 링크</h1>
        <button
          className="mgmt-btn mgmt-btn-primary"
          onClick={handleCreateLink}
          disabled={createLinkLoading}
        >
          {createLinkLoading ? "생성 중..." : "+ 링크 생성"}
        </button>
      </div>

      {newLinkUrl && (
        <div className="mgmt-form">
          <p className="mgmt-link-label">새 링크가 생성되었습니다:</p>
          <div className="mgmt-link-url-box">
            <input
              type="text"
              value={newLinkUrl}
              readOnly
              className="mgmt-link-url-input"
              onClick={(e) => (e.target as HTMLInputElement).select()}
            />
            <button
              className="mgmt-btn mgmt-btn-primary"
              onClick={() => handleCopyUrl(newLinkUrl)}
            >
              {copySuccess ? "복사됨" : "복사"}
            </button>
          </div>
        </div>
      )}

      {linksLoading ? (
        <p className="mgmt-loading">로딩 중...</p>
      ) : linksError ? (
        <p className="mgmt-error">{linksError}</p>
      ) : tempLinks.length === 0 ? (
        <p className="mgmt-empty">활성 임시 링크가 없습니다</p>
      ) : (
        <div className="mgmt-list">
          {tempLinks.map((link) => (
            <div key={link.id} className="mgmt-card">
              <div className="mgmt-card-info">
                <span className="mgmt-card-name">
                  {link.label || "임시 링크"}
                </span>
                <span className="mgmt-card-phone">
                  생성: {formatKstDateTime(link.createdAt)}
                </span>
                <span className="mgmt-card-phone">
                  만료: {formatKstDateTime(link.expiresAt)}
                </span>
              </div>
              <div className="mgmt-card-actions">
                <button
                  className="mgmt-btn mgmt-btn-small mgmt-btn-danger"
                  onClick={() => {
                    setRevokeTarget(link);
                    setActionError(null);
                  }}
                >
                  폐기
                </button>
              </div>
            </div>
          ))}
        </div>
      )}

      {revokeTarget && (
        <Modal
          onClose={() => {
            setRevokeTarget(null);
            setActionError(null);
          }}
          ariaLabel="임시 링크 폐기 확인"
        >
          <p className="mgmt-modal-text">
            이 임시 링크를 폐기하시겠습니까?
            <br />
            <small>폐기 후에는 해당 링크로 접속할 수 없습니다.</small>
          </p>
          {actionError && <p className="mgmt-form-error">{actionError}</p>}
          <div className="mgmt-form-actions">
            <button
              className="mgmt-btn mgmt-btn-danger"
              onClick={handleRevoke}
              disabled={revokeLoading}
            >
              {revokeLoading ? "폐기 중..." : "폐기"}
            </button>
            <button
              className="mgmt-btn mgmt-btn-secondary"
              onClick={() => {
                setRevokeTarget(null);
                setActionError(null);
              }}
            >
              취소
            </button>
          </div>
        </Modal>
      )}
    </div>
  );
}
