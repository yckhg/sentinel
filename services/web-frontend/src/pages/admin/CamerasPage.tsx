import { useEffect, useState } from "react";
import { fetchWithTimeout, isTimeoutError, timeoutMessage } from "../../utils/fetchWithTimeout";
import Modal from "../../components/Modal";
import { navigate } from "../../utils/navigation";

interface Camera {
  id: number;
  name: string;
  location: string;
  zone: string;
  streamKey: string;
  sourceType: string;
  sourceUrl: string;
  enabled: boolean;
  hlsUrl: string;
  status: string;
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

/**
 * /admin/cameras — camera management subpage.
 *
 * Behavior relocated verbatim from the former ManagementPage "카메라 관리"
 * section (list / add / edit(enabled toggle) / delete-with-confirm, plus the
 * loading·error·empty states). The `/api/cameras` request/response contract and
 * admin auth headers are unchanged; the admin gate is inherited from the seam.
 */
export default function CamerasPage() {
  // Camera management state
  const [cameras, setCameras] = useState<Camera[]>([]);
  const [camerasLoading, setCamerasLoading] = useState(true);
  const [camerasError, setCamerasError] = useState<string | null>(null);
  const [showCameraAddForm, setShowCameraAddForm] = useState(false);
  const [camAddName, setCamAddName] = useState("");
  const [camAddLocation, setCamAddLocation] = useState("");
  const [camAddZone, setCamAddZone] = useState("");
  const [camAddSourceType, setCamAddSourceType] = useState("rtsp");
  const [camAddSourceUrl, setCamAddSourceUrl] = useState("");
  const [camAddError, setCamAddError] = useState<string | null>(null);
  const [camAddLoading, setCamAddLoading] = useState(false);
  const [camEditId, setCamEditId] = useState<number | null>(null);
  const [camEditName, setCamEditName] = useState("");
  const [camEditLocation, setCamEditLocation] = useState("");
  const [camEditZone, setCamEditZone] = useState("");
  const [camEditSourceType, setCamEditSourceType] = useState("rtsp");
  const [camEditSourceUrl, setCamEditSourceUrl] = useState("");
  const [camEditEnabled, setCamEditEnabled] = useState(true);
  const [camEditError, setCamEditError] = useState<string | null>(null);
  const [camEditLoading, setCamEditLoading] = useState(false);
  const [camDeleteTarget, setCamDeleteTarget] = useState<Camera | null>(null);
  const [camDeleteLoading, setCamDeleteLoading] = useState(false);
  const [camDeleteError, setCamDeleteError] = useState<string | null>(null);

  const fetchCameras = async () => {
    try {
      const res = await fetchWithTimeout("/api/cameras", { headers: getAuthHeaders() });
      if (!res.ok) throw new Error(`HTTP ${res.status}`);
      const data: Camera[] = await res.json();
      setCameras(data);
      setCamerasError(null);
    } catch (err) {
      setCamerasError(
        isTimeoutError(err)
          ? timeoutMessage()
          : err instanceof Error ? err.message : "카메라 목록을 불러올 수 없습니다"
      );
    } finally {
      setCamerasLoading(false);
    }
  };

  const handleCameraAdd = async () => {
    setCamAddError(null);
    if (!camAddName.trim()) { setCamAddError("이름을 입력하세요"); return; }
    setCamAddLoading(true);
    try {
      const res = await fetchWithTimeout("/api/cameras", {
        method: "POST",
        headers: getAuthHeaders(),
        body: JSON.stringify({
          name: camAddName.trim(),
          location: camAddLocation.trim(),
          zone: camAddZone.trim(),
          sourceType: camAddSourceType,
          sourceUrl: camAddSourceUrl.trim(),
        }),
      });
      if (!res.ok) {
        const body = await res.json().catch(() => null);
        throw new Error(body?.error || `HTTP ${res.status}`);
      }
      setCamAddName(""); setCamAddLocation(""); setCamAddZone("");
      setCamAddSourceType("rtsp"); setCamAddSourceUrl("");
      setShowCameraAddForm(false);
      await fetchCameras();
    } catch (err) {
      setCamAddError(isTimeoutError(err) ? timeoutMessage() : err instanceof Error ? err.message : "추가 실패");
    } finally {
      setCamAddLoading(false);
    }
  };

  const startCameraEdit = (cam: Camera) => {
    setCamEditId(cam.id);
    setCamEditName(cam.name);
    setCamEditLocation(cam.location);
    setCamEditZone(cam.zone);
    setCamEditSourceType(cam.sourceType);
    setCamEditSourceUrl(cam.sourceUrl);
    setCamEditEnabled(cam.enabled);
    setCamEditError(null);
  };

  const cancelCameraEdit = () => {
    setCamEditId(null);
    setCamEditError(null);
  };

  const handleCameraEdit = async () => {
    if (camEditId === null) return;
    setCamEditError(null);
    if (!camEditName.trim()) { setCamEditError("이름을 입력하세요"); return; }
    setCamEditLoading(true);
    try {
      const res = await fetchWithTimeout(`/api/cameras/${camEditId}`, {
        method: "PUT",
        headers: getAuthHeaders(),
        body: JSON.stringify({
          name: camEditName.trim(),
          location: camEditLocation.trim(),
          zone: camEditZone.trim(),
          sourceType: camEditSourceType,
          sourceUrl: camEditSourceUrl.trim(),
          enabled: camEditEnabled,
        }),
      });
      if (!res.ok) {
        const body = await res.json().catch(() => null);
        throw new Error(body?.error || `HTTP ${res.status}`);
      }
      setCamEditId(null);
      await fetchCameras();
    } catch (err) {
      setCamEditError(isTimeoutError(err) ? timeoutMessage() : err instanceof Error ? err.message : "수정 실패");
    } finally {
      setCamEditLoading(false);
    }
  };

  const handleCameraDelete = async () => {
    if (!camDeleteTarget) return;
    setCamDeleteLoading(true);
    setCamDeleteError(null);
    try {
      const res = await fetchWithTimeout(`/api/cameras/${camDeleteTarget.id}`, {
        method: "DELETE",
        headers: getAuthHeaders(),
      });
      if (!res.ok && res.status !== 204) throw new Error(`HTTP ${res.status}`);
      setCamDeleteTarget(null);
      await fetchCameras();
    } catch (err) {
      setCamDeleteError(errorMessage(err));
    } finally {
      setCamDeleteLoading(false);
    }
  };

  useEffect(() => {
    fetchCameras();
  }, []);

  return (
    <div className="admin-page" data-testid="admin-page" data-slug="cameras">
      <button
        type="button"
        className="admin-back"
        data-testid="admin-back"
        onClick={() => navigate("/admin")}
      >
        ← 관리
      </button>
      <h1 className="admin-page-title">카메라 관리</h1>

      <div className="mgmt-header">
        <h2>카메라 관리</h2>
        {!showCameraAddForm && (
          <button
            className="mgmt-btn mgmt-btn-primary"
            onClick={() => { setShowCameraAddForm(true); setCamAddError(null); }}
          >
            + 추가
          </button>
        )}
      </div>

      {/* Camera add form */}
      {showCameraAddForm && (
        <div className="mgmt-form">
          <div className="mgmt-form-field">
            <label>이름</label>
            <input type="text" value={camAddName} onChange={(e) => setCamAddName(e.target.value)} placeholder="카메라 이름" autoFocus />
          </div>
          <div className="mgmt-form-field">
            <label>위치</label>
            <input type="text" value={camAddLocation} onChange={(e) => setCamAddLocation(e.target.value)} placeholder="설치 위치" />
          </div>
          <div className="mgmt-form-field">
            <label>구역</label>
            <input type="text" value={camAddZone} onChange={(e) => setCamAddZone(e.target.value)} placeholder="공장 1동 프레스 구역" />
          </div>
          <div className="mgmt-form-field">
            <label>소스 타입</label>
            <select className="mgmt-select" value={camAddSourceType} onChange={(e) => setCamAddSourceType(e.target.value)}>
              <option value="rtsp">RTSP</option>
              <option value="youtube">YouTube</option>
            </select>
          </div>
          <div className="mgmt-form-field">
            <label>소스 URL</label>
            <input type="text" value={camAddSourceUrl} onChange={(e) => setCamAddSourceUrl(e.target.value)} placeholder={camAddSourceType === "rtsp" ? "rtsp://..." : "https://youtube.com/..."} />
          </div>
          {camAddError && <p className="mgmt-form-error">{camAddError}</p>}
          <div className="mgmt-form-actions">
            <button className="mgmt-btn mgmt-btn-primary" onClick={handleCameraAdd} disabled={camAddLoading}>
              {camAddLoading ? "저장 중..." : "저장"}
            </button>
            <button className="mgmt-btn mgmt-btn-secondary" onClick={() => {
              setShowCameraAddForm(false); setCamAddName(""); setCamAddLocation(""); setCamAddZone("");
              setCamAddSourceType("rtsp"); setCamAddSourceUrl(""); setCamAddError(null);
            }}>
              취소
            </button>
          </div>
        </div>
      )}

      {/* Camera list */}
      {camerasLoading ? (
        <p className="mgmt-loading">로딩 중...</p>
      ) : camerasError ? (
        <p className="mgmt-error">{camerasError}</p>
      ) : cameras.length === 0 ? (
        <p className="mgmt-empty">등록된 카메라가 없습니다</p>
      ) : (
        <div className="mgmt-list">
          {cameras.map((cam) =>
            camEditId === cam.id ? (
              <div key={cam.id} className="mgmt-card mgmt-card-editing">
                <div className="mgmt-form-field">
                  <label>이름</label>
                  <input type="text" value={camEditName} onChange={(e) => setCamEditName(e.target.value)} autoFocus />
                </div>
                <div className="mgmt-form-field">
                  <label>위치</label>
                  <input type="text" value={camEditLocation} onChange={(e) => setCamEditLocation(e.target.value)} />
                </div>
                <div className="mgmt-form-field">
                  <label>구역</label>
                  <input type="text" value={camEditZone} onChange={(e) => setCamEditZone(e.target.value)} />
                </div>
                <div className="mgmt-form-field">
                  <label>소스 타입</label>
                  <select className="mgmt-select" value={camEditSourceType} onChange={(e) => setCamEditSourceType(e.target.value)}>
                    <option value="rtsp">RTSP</option>
                    <option value="youtube">YouTube</option>
                  </select>
                </div>
                <div className="mgmt-form-field">
                  <label>소스 URL</label>
                  <input type="text" value={camEditSourceUrl} onChange={(e) => setCamEditSourceUrl(e.target.value)} />
                </div>
                <div className="mgmt-form-field">
                  <label className="mgmt-checkbox-label">
                    <input type="checkbox" checked={camEditEnabled} onChange={(e) => setCamEditEnabled(e.target.checked)} />
                    활성화
                  </label>
                </div>
                {camEditError && <p className="mgmt-form-error">{camEditError}</p>}
                <div className="mgmt-form-actions">
                  <button className="mgmt-btn mgmt-btn-primary" onClick={handleCameraEdit} disabled={camEditLoading}>
                    {camEditLoading ? "저장 중..." : "저장"}
                  </button>
                  <button className="mgmt-btn mgmt-btn-secondary" onClick={cancelCameraEdit}>취소</button>
                </div>
              </div>
            ) : (
              <div key={cam.id} className="mgmt-card">
                <div className="mgmt-card-info">
                  <span className="mgmt-card-name">
                    {cam.name}
                    <span className={`mgmt-badge-source mgmt-badge-${cam.sourceType}`}>
                      {cam.sourceType.toUpperCase()}
                    </span>
                    {!cam.enabled && <span className="mgmt-badge-disabled">비활성</span>}
                  </span>
                  <span className="mgmt-card-phone">
                    {cam.location}{cam.zone ? ` / ${cam.zone}` : ""}
                  </span>
                </div>
                <div className="mgmt-card-actions">
                  <button className="mgmt-btn mgmt-btn-small" onClick={() => startCameraEdit(cam)}>수정</button>
                  <button className="mgmt-btn mgmt-btn-small mgmt-btn-danger" onClick={() => setCamDeleteTarget(cam)}>삭제</button>
                </div>
              </div>
            )
          )}
        </div>
      )}

      {/* Camera delete confirmation dialog */}
      {camDeleteTarget && (
        <Modal
          onClose={() => { setCamDeleteTarget(null); setCamDeleteError(null); }}
          ariaLabel="카메라 삭제 확인"
        >
          <p className="mgmt-modal-text">
            <strong>{camDeleteTarget.name}</strong> 카메라를 삭제하시겠습니까?
          </p>
          {camDeleteError && <p className="mgmt-form-error">{camDeleteError}</p>}
          <div className="mgmt-form-actions">
            <button className="mgmt-btn mgmt-btn-danger" onClick={handleCameraDelete} disabled={camDeleteLoading}>
              {camDeleteLoading ? "삭제 중..." : "삭제"}
            </button>
            <button className="mgmt-btn mgmt-btn-secondary" onClick={() => { setCamDeleteTarget(null); setCamDeleteError(null); }}>취소</button>
          </div>
        </Modal>
      )}
    </div>
  );
}
