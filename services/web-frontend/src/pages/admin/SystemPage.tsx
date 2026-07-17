import { useEffect, useState } from "react";
import { navigate } from "../../utils/navigation";
import {
  fetchWithTimeout,
  isTimeoutError,
  timeoutMessage,
} from "../../utils/fetchWithTimeout";
import { PHONE_REGEX, formatPhoneInput } from "../../utils/phone";

interface Site {
  id: number;
  address: string;
  managerName: string;
  managerPhone: string;
}

function getAuthHeaders(): HeadersInit {
  const token = localStorage.getItem("token");
  return token
    ? { Authorization: `Bearer ${token}`, "Content-Type": "application/json" }
    : { "Content-Type": "application/json" };
}

// page-system leaf (admin-IA): "현장 정보" + "시스템 설정" relocated from
// ManagementPage into the self-contained /admin/system subpage. Behavior of both
// sections is preserved; the admin-only mount replaces the old showAccounts gate.
export default function SystemPage() {
  // Site info state
  const [sites, setSites] = useState<Site[]>([]);
  const [sitesLoading, setSitesLoading] = useState(true);
  const [sitesError, setSitesError] = useState<string | null>(null);
  const [siteEditId, setSiteEditId] = useState<number | null>(null);
  const [siteEditAddress, setSiteEditAddress] = useState("");
  const [siteEditManagerName, setSiteEditManagerName] = useState("");
  const [siteEditManagerPhone, setSiteEditManagerPhone] = useState("");
  const [siteEditError, setSiteEditError] = useState<string | null>(null);
  const [siteEditLoading, setSiteEditLoading] = useState(false);

  // System settings state
  const [siteUrl, setSiteUrl] = useState("");
  const [siteUrlOriginal, setSiteUrlOriginal] = useState("");
  const [settingsLoading, setSettingsLoading] = useState(true);
  const [settingsError, setSettingsError] = useState<string | null>(null);
  const [settingsSaving, setSettingsSaving] = useState(false);
  const [settingsSuccess, setSettingsSuccess] = useState<string | null>(null);

  const fetchSites = async () => {
    try {
      const res = await fetchWithTimeout("/api/sites", { headers: getAuthHeaders() });
      if (!res.ok) throw new Error(`HTTP ${res.status}`);
      const data: Site[] = await res.json();
      setSites(data);
      setSitesError(null);
    } catch (err) {
      setSitesError(
        isTimeoutError(err)
          ? timeoutMessage()
          : err instanceof Error ? err.message : "현장 정보를 불러올 수 없습니다"
      );
    } finally {
      setSitesLoading(false);
    }
  };

  const fetchSettings = async () => {
    try {
      const res = await fetchWithTimeout("/api/settings", { headers: getAuthHeaders() });
      if (!res.ok) throw new Error(`HTTP ${res.status}`);
      const data: { key: string; value: string }[] = await res.json();
      const siteUrlSetting = data.find((s) => s.key === "site_url");
      const val = siteUrlSetting?.value || "";
      setSiteUrl(val);
      setSiteUrlOriginal(val);
      setSettingsError(null);
    } catch (err) {
      setSettingsError(
        isTimeoutError(err)
          ? timeoutMessage()
          : err instanceof Error ? err.message : "설정을 불러올 수 없습니다"
      );
    } finally {
      setSettingsLoading(false);
    }
  };

  useEffect(() => {
    fetchSites();
    fetchSettings();
  }, []);

  const startSiteEdit = (site: Site) => {
    setSiteEditId(site.id);
    setSiteEditAddress(site.address);
    setSiteEditManagerName(site.managerName);
    setSiteEditManagerPhone(site.managerPhone);
    setSiteEditError(null);
  };

  const cancelSiteEdit = () => {
    setSiteEditId(null);
    setSiteEditError(null);
  };

  const handleSiteEdit = async () => {
    if (siteEditId === null) return;
    setSiteEditError(null);
    if (!siteEditAddress.trim()) {
      setSiteEditError("주소를 입력하세요");
      return;
    }
    if (!siteEditManagerName.trim()) {
      setSiteEditError("담당자 이름을 입력하세요");
      return;
    }
    if (!PHONE_REGEX.test(siteEditManagerPhone)) {
      setSiteEditError("전화번호 형식이 올바르지 않습니다 (예: 010-1234-5678)");
      return;
    }
    setSiteEditLoading(true);
    try {
      const res = await fetchWithTimeout(`/api/sites/${siteEditId}`, {
        method: "PUT",
        headers: getAuthHeaders(),
        body: JSON.stringify({
          address: siteEditAddress.trim(),
          managerName: siteEditManagerName.trim(),
          managerPhone: siteEditManagerPhone,
        }),
      });
      if (!res.ok) {
        const body = await res.json().catch(() => null);
        throw new Error(body?.error || `HTTP ${res.status}`);
      }
      setSiteEditId(null);
      await fetchSites();
    } catch (err) {
      setSiteEditError(
        isTimeoutError(err)
          ? timeoutMessage()
          : err instanceof Error ? err.message : "수정 실패"
      );
    } finally {
      setSiteEditLoading(false);
    }
  };

  const handleSaveSettings = async () => {
    setSettingsSaving(true);
    setSettingsError(null);
    setSettingsSuccess(null);
    try {
      const res = await fetchWithTimeout("/api/settings/site_url", {
        method: "PUT",
        headers: getAuthHeaders(),
        body: JSON.stringify({ value: siteUrl.trim() }),
      });
      if (!res.ok) {
        const data = await res.json().catch(() => ({}));
        throw new Error(data.error || `HTTP ${res.status}`);
      }
      setSiteUrlOriginal(siteUrl.trim());
      setSiteUrl(siteUrl.trim());
      setSettingsSuccess("저장되었습니다");
      setTimeout(() => setSettingsSuccess(null), 3000);
    } catch (err) {
      setSettingsError(err instanceof Error ? err.message : "저장에 실패했습니다");
    } finally {
      setSettingsSaving(false);
    }
  };

  return (
    <div className="admin-page" data-testid="admin-page" data-slug="system">
      <button
        type="button"
        className="admin-back"
        data-testid="admin-back"
        onClick={() => navigate("/admin")}
      >
        ← 관리
      </button>
      <h1 className="admin-page-title">시스템 설정</h1>

      {/* Site info section */}
      <div className="mgmt-header">
        <h2>현장 정보</h2>
      </div>
      {sitesLoading ? (
        <p className="mgmt-loading">로딩 중...</p>
      ) : sitesError ? (
        <p className="mgmt-error">{sitesError}</p>
      ) : sites.length === 0 ? (
        <p className="mgmt-empty">등록된 현장 정보가 없습니다</p>
      ) : (
        <div className="mgmt-list">
          {sites.map((site) =>
            siteEditId === site.id ? (
              <div key={site.id} className="mgmt-card mgmt-card-editing">
                <div className="mgmt-form-field">
                  <label>주소</label>
                  <input
                    type="text"
                    value={siteEditAddress}
                    onChange={(e) => setSiteEditAddress(e.target.value)}
                    placeholder="현장 주소"
                    autoFocus
                  />
                </div>
                <div className="mgmt-form-field">
                  <label>담당자 이름</label>
                  <input
                    type="text"
                    value={siteEditManagerName}
                    onChange={(e) => setSiteEditManagerName(e.target.value)}
                    placeholder="홍길동"
                  />
                </div>
                <div className="mgmt-form-field">
                  <label>담당자 전화번호</label>
                  <input
                    type="tel"
                    value={siteEditManagerPhone}
                    onChange={(e) =>
                      setSiteEditManagerPhone(formatPhoneInput(e.target.value))
                    }
                    placeholder="010-1234-5678"
                    maxLength={13}
                  />
                </div>
                {siteEditError && (
                  <p className="mgmt-form-error">{siteEditError}</p>
                )}
                <div className="mgmt-form-actions">
                  <button
                    className="mgmt-btn mgmt-btn-primary"
                    onClick={handleSiteEdit}
                    disabled={siteEditLoading}
                  >
                    {siteEditLoading ? "저장 중..." : "저장"}
                  </button>
                  <button
                    className="mgmt-btn mgmt-btn-secondary"
                    onClick={cancelSiteEdit}
                  >
                    취소
                  </button>
                </div>
              </div>
            ) : (
              <div key={site.id} className="mgmt-card">
                <div className="mgmt-card-info">
                  <span className="mgmt-card-name">{site.address || "주소 미등록"}</span>
                  <span className="mgmt-card-phone">
                    담당자: {site.managerName || "-"} / {site.managerPhone || "-"}
                  </span>
                </div>
                <div className="mgmt-card-actions">
                  <button
                    className="mgmt-btn mgmt-btn-small"
                    onClick={() => startSiteEdit(site)}
                  >
                    수정
                  </button>
                </div>
              </div>
            )
          )}
        </div>
      )}

      <div className="mgmt-section-divider" />

      {/* System settings section */}
      <div className="mgmt-header">
        <h2>시스템 설정</h2>
      </div>
      {settingsLoading ? (
        <p className="mgmt-loading">로딩 중...</p>
      ) : settingsError ? (
        <p className="mgmt-error">{settingsError}</p>
      ) : (
        <div className="mgmt-form">
          <div className="mgmt-form-field">
            <label>외부 접속 URL (SITE_URL)</label>
            <input
              type="url"
              value={siteUrl}
              onChange={(e) => {
                setSiteUrl(e.target.value);
                setSettingsSuccess(null);
              }}
              placeholder="https://example.com:20006"
            />
            <span className="mgmt-form-hint">
              알림 메시지의 CCTV 링크에 사용됩니다. 비워두면 기본값을 사용합니다.
            </span>
          </div>
          {settingsSuccess && (
            <p className="mgmt-form-success">{settingsSuccess}</p>
          )}
          <div className="mgmt-form-actions">
            <button
              className="mgmt-btn mgmt-btn-primary"
              onClick={handleSaveSettings}
              disabled={settingsSaving || siteUrl === siteUrlOriginal}
            >
              {settingsSaving ? "저장 중..." : "저장"}
            </button>
            {siteUrl !== siteUrlOriginal && (
              <button
                className="mgmt-btn mgmt-btn-secondary"
                onClick={() => {
                  setSiteUrl(siteUrlOriginal);
                  setSettingsSuccess(null);
                  setSettingsError(null);
                }}
              >
                취소
              </button>
            )}
          </div>
        </div>
      )}
    </div>
  );
}
