import { useEffect, useState } from "react";
import { fetchWithTimeout, isTimeoutError, timeoutMessage } from "../utils/fetchWithTimeout";
import DevicesSection from "../components/DevicesSection";
import HealthPanel from "../components/HealthPanel";

interface Contact {
  id: number;
  name: string;
  phone: string;
  email: string;
  notifyEmail: boolean;
}

interface Site {
  id: number;
  address: string;
  managerName: string;
  managerPhone: string;
}

interface TempLink {
  id: string;
  label: string;
  createdAt: string;
  expiresAt: string;
  url?: string;
}

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

interface Invitation {
  id: number;
  email: string;
  token: string;
  status: string;
  createdAt: string;
  expiresAt: string;
}

interface PendingUser {
  id: number;
  username: string;
  name: string;
  status: string;
  createdAt: string;
}

interface ActiveUser {
  id: number;
  username: string;
  name: string;
  role: string;
  createdAt: string;
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

const PHONE_REGEX = /^01[016789]-\d{3,4}-\d{4}$/;

function formatPhoneInput(value: string): string {
  const digits = value.replace(/\D/g, "");
  if (digits.length <= 3) return digits;
  if (digits.length <= 7) return `${digits.slice(0, 3)}-${digits.slice(3)}`;
  return `${digits.slice(0, 3)}-${digits.slice(3, 7)}-${digits.slice(7, 11)}`;
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

export default function ManagementPage() {
  const [contacts, setContacts] = useState<Contact[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

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

  // Add form
  const [showAddForm, setShowAddForm] = useState(false);
  const [addName, setAddName] = useState("");
  const [addPhone, setAddPhone] = useState("");
  const [addEmail, setAddEmail] = useState("");
  const [addNotifyEmail, setAddNotifyEmail] = useState(false);
  const [addError, setAddError] = useState<string | null>(null);
  const [addLoading, setAddLoading] = useState(false);

  // Edit state
  const [editId, setEditId] = useState<number | null>(null);
  const [editName, setEditName] = useState("");
  const [editPhone, setEditPhone] = useState("");
  const [editEmail, setEditEmail] = useState("");
  const [editNotifyEmail, setEditNotifyEmail] = useState(false);
  const [editError, setEditError] = useState<string | null>(null);
  const [editLoading, setEditLoading] = useState(false);

  // Delete confirmation
  const [deleteTarget, setDeleteTarget] = useState<Contact | null>(null);
  const [deleteLoading, setDeleteLoading] = useState(false);

  // Temp links state
  const [tempLinks, setTempLinks] = useState<TempLink[]>([]);
  const [linksLoading, setLinksLoading] = useState(true);
  const [linksError, setLinksError] = useState<string | null>(null);
  const [createLinkLoading, setCreateLinkLoading] = useState(false);
  const [newLinkUrl, setNewLinkUrl] = useState<string | null>(null);
  const [copySuccess, setCopySuccess] = useState(false);
  const [revokeTarget, setRevokeTarget] = useState<TempLink | null>(null);
  const [revokeLoading, setRevokeLoading] = useState(false);

  // Account management state
  const [showAccounts] = useState(isAdmin());
  const [pendingUsers, setPendingUsers] = useState<PendingUser[]>([]);
  const [activeUsers, setActiveUsers] = useState<ActiveUser[]>([]);
  const [accountsLoading, setAccountsLoading] = useState(true);
  const [accountsError, setAccountsError] = useState<string | null>(null);
  const [approveLoading, setApproveLoading] = useState<number | null>(null);
  const [rejectLoading, setRejectLoading] = useState<number | null>(null);

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

  // Invitation management state
  const [invitations, setInvitations] = useState<Invitation[]>([]);
  const [invitationsLoading, setInvitationsLoading] = useState(true);
  const [invitationsError, setInvitationsError] = useState<string | null>(null);
  const [inviteEmail, setInviteEmail] = useState("");
  const [inviteLoading, setInviteLoading] = useState(false);
  const [inviteError, setInviteError] = useState<string | null>(null);
  const [inviteSuccess, setInviteSuccess] = useState<string | null>(null);
  const [cancelInviteTarget, setCancelInviteTarget] = useState<Invitation | null>(null);
  const [cancelInviteLoading, setCancelInviteLoading] = useState(false);

  // Test alert simulation
  const [testAlertLoading, setTestAlertLoading] = useState(false);
  const [testAlertError, setTestAlertError] = useState<string | null>(null);
  const [testAlertSuccess, setTestAlertSuccess] = useState<string | null>(null);

  // System settings state
  const [siteUrl, setSiteUrl] = useState("");
  const [siteUrlOriginal, setSiteUrlOriginal] = useState("");
  const [settingsLoading, setSettingsLoading] = useState(true);
  const [settingsError, setSettingsError] = useState<string | null>(null);
  const [settingsSaving, setSettingsSaving] = useState(false);
  const [settingsSuccess, setSettingsSuccess] = useState<string | null>(null);

  // Storage & archives state
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
  const [storageStats, setStorageStats] = useState<StorageStats | null>(null);
  const [storageLoading, setStorageLoading] = useState(true);
  const [storageError, setStorageError] = useState<string | null>(null);
  const [archives, setArchives] = useState<Archive[]>([]);
  const [archivesLoading, setArchivesLoading] = useState(true);
  const [archiveDeleteTarget, setArchiveDeleteTarget] = useState<Archive | null>(null);
  const [archiveDeleteLoading, setArchiveDeleteLoading] = useState(false);
  const [incidentDeleteTarget, setIncidentDeleteTarget] = useState<string | null>(null);
  const [incidentDeleteLoading, setIncidentDeleteLoading] = useState(false);
  const [archiveDownloading, setArchiveDownloading] = useState<string | null>(null);

  const fetchContacts = async () => {
    try {
      const res = await fetchWithTimeout("/api/contacts", { headers: getAuthHeaders() });
      if (!res.ok) throw new Error(`HTTP ${res.status}`);
      const data: Contact[] = await res.json();
      setContacts(data);
      setError(null);
    } catch (err) {
      setError(
        isTimeoutError(err)
          ? timeoutMessage()
          : err instanceof Error ? err.message : "연락처를 불러올 수 없습니다"
      );
    } finally {
      setLoading(false);
    }
  };

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

  const fetchTempLinks = async () => {
    try {
      const res = await fetchWithTimeout("/api/links", { headers: getAuthHeaders() });
      if (!res.ok) throw new Error(`HTTP ${res.status}`);
      const data: TempLink[] = await res.json();
      setTempLinks(data || []);
      setLinksError(null);
    } catch (err) {
      setLinksError(
        isTimeoutError(err)
          ? timeoutMessage()
          : err instanceof Error ? err.message : "임시 링크를 불러올 수 없습니다"
      );
    } finally {
      setLinksLoading(false);
    }
  };

  const fetchPendingUsers = async () => {
    try {
      const res = await fetchWithTimeout("/auth/pending", { headers: getAuthHeaders() });
      if (!res.ok) throw new Error(`HTTP ${res.status}`);
      const data: PendingUser[] = await res.json();
      setPendingUsers(data || []);
    } catch (err) {
      setAccountsError(
        isTimeoutError(err)
          ? timeoutMessage()
          : err instanceof Error ? err.message : "계정 정보를 불러올 수 없습니다"
      );
    }
  };

  const fetchActiveUsers = async () => {
    try {
      const res = await fetchWithTimeout("/auth/users", { headers: getAuthHeaders() });
      if (!res.ok) throw new Error(`HTTP ${res.status}`);
      const data: ActiveUser[] = await res.json();
      setActiveUsers(data || []);
    } catch (err) {
      setAccountsError(
        isTimeoutError(err)
          ? timeoutMessage()
          : err instanceof Error ? err.message : "계정 정보를 불러올 수 없습니다"
      );
    }
  };

  const fetchAccounts = async () => {
    setAccountsLoading(true);
    setAccountsError(null);
    await Promise.all([fetchPendingUsers(), fetchActiveUsers()]);
    setAccountsLoading(false);
  };

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

  const fetchInvitations = async () => {
    try {
      const res = await fetchWithTimeout("/api/invitations", { headers: getAuthHeaders() });
      if (!res.ok) throw new Error(`HTTP ${res.status}`);
      const data: Invitation[] = await res.json();
      setInvitations(data || []);
      setInvitationsError(null);
    } catch (err) {
      setInvitationsError(
        isTimeoutError(err)
          ? timeoutMessage()
          : err instanceof Error ? err.message : "초대 목록을 불러올 수 없습니다"
      );
    } finally {
      setInvitationsLoading(false);
    }
  };

  const handleSendInvite = async () => {
    setInviteError(null);
    setInviteSuccess(null);
    const email = inviteEmail.trim();
    if (!email || !email.includes("@")) {
      setInviteError("유효한 이메일을 입력하세요");
      return;
    }
    setInviteLoading(true);
    try {
      const res = await fetchWithTimeout("/api/invitations", {
        method: "POST",
        headers: getAuthHeaders(),
        body: JSON.stringify({ email }),
      });
      if (!res.ok) {
        const body = await res.json().catch(() => null);
        throw new Error(body?.error || `HTTP ${res.status}`);
      }
      setInviteEmail("");
      setInviteSuccess(`${email}에 초대 이메일을 발송했습니다`);
      await fetchInvitations();
    } catch (err) {
      setInviteError(isTimeoutError(err) ? timeoutMessage() : err instanceof Error ? err.message : "초대 실패");
    } finally {
      setInviteLoading(false);
    }
  };

  const handleCancelInvite = async () => {
    if (!cancelInviteTarget) return;
    setCancelInviteLoading(true);
    try {
      const res = await fetchWithTimeout(`/api/invitations/${cancelInviteTarget.id}`, {
        method: "DELETE",
        headers: getAuthHeaders(),
      });
      if (!res.ok && res.status !== 204) throw new Error(`HTTP ${res.status}`);
      setCancelInviteTarget(null);
      await fetchInvitations();
    } catch {
      setCancelInviteTarget(null);
    } finally {
      setCancelInviteLoading(false);
    }
  };

  const handleTestAlert = async () => {
    setTestAlertError(null);
    setTestAlertSuccess(null);
    setTestAlertLoading(true);
    try {
      const res = await fetchWithTimeout("/api/test-alert", {
        method: "POST",
        headers: getAuthHeaders(),
      });
      if (!res.ok) {
        const data = await res.json().catch(() => ({}));
        throw new Error(data.error || `HTTP ${res.status}`);
      }
      setTestAlertSuccess("테스트 비상 신호가 발송되었습니다. 사고 이력 탭에서 확인하세요.");
      setTimeout(() => setTestAlertSuccess(null), 5000);
    } catch (err) {
      setTestAlertError(
        isTimeoutError(err) ? timeoutMessage() : `테스트 발송 실패: ${err instanceof Error ? err.message : "알 수 없는 오류"}`
      );
    } finally {
      setTestAlertLoading(false);
    }
  };

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
    } catch {
      // non-critical
    } finally {
      setArchivesLoading(false);
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

  const handleArchiveDelete = async () => {
    if (!archiveDeleteTarget) return;
    setArchiveDeleteLoading(true);
    try {
      const res = await fetchWithTimeout(`/api/archives/${archiveDeleteTarget.id}`, {
        method: "DELETE",
        headers: getAuthHeaders(),
      });
      if (!res.ok && res.status !== 204) throw new Error(`HTTP ${res.status}`);
      setArchiveDeleteTarget(null);
      await Promise.all([fetchArchives(), fetchStorage()]);
    } catch {
      setArchiveDeleteTarget(null);
    } finally {
      setArchiveDeleteLoading(false);
    }
  };

  const handleIncidentArchiveDelete = async () => {
    if (!incidentDeleteTarget) return;
    setIncidentDeleteLoading(true);
    try {
      const res = await fetchWithTimeout(`/api/archives/incident/${encodeURIComponent(incidentDeleteTarget)}`, {
        method: "DELETE",
        headers: getAuthHeaders(),
      });
      if (!res.ok && res.status !== 204) throw new Error(`HTTP ${res.status}`);
      setIncidentDeleteTarget(null);
      await Promise.all([fetchArchives(), fetchStorage()]);
    } catch {
      setIncidentDeleteTarget(null);
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
    try {
      const res = await fetchWithTimeout(`/api/cameras/${camDeleteTarget.id}`, {
        method: "DELETE",
        headers: getAuthHeaders(),
      });
      if (!res.ok && res.status !== 204) throw new Error(`HTTP ${res.status}`);
      setCamDeleteTarget(null);
      await fetchCameras();
    } catch {
      setCamDeleteTarget(null);
    } finally {
      setCamDeleteLoading(false);
    }
  };

  const handleApprove = async (userId: number) => {
    setApproveLoading(userId);
    try {
      const res = await fetchWithTimeout(`/auth/approve/${userId}`, {
        method: "POST",
        headers: getAuthHeaders(),
      });
      if (!res.ok) {
        const body = await res.json().catch(() => null);
        throw new Error(body?.error || `HTTP ${res.status}`);
      }
      await fetchAccounts();
    } catch (err) {
      setAccountsError(isTimeoutError(err) ? timeoutMessage() : err instanceof Error ? err.message : "승인 실패");
    } finally {
      setApproveLoading(null);
    }
  };

  const handleReject = async (userId: number) => {
    setRejectLoading(userId);
    try {
      const res = await fetchWithTimeout(`/auth/reject/${userId}`, {
        method: "POST",
        headers: getAuthHeaders(),
      });
      if (!res.ok) {
        const body = await res.json().catch(() => null);
        throw new Error(body?.error || `HTTP ${res.status}`);
      }
      await fetchAccounts();
    } catch (err) {
      setAccountsError(isTimeoutError(err) ? timeoutMessage() : err instanceof Error ? err.message : "거절 실패");
    } finally {
      setRejectLoading(null);
    }
  };

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
      setLinksError(isTimeoutError(err) ? timeoutMessage() : err instanceof Error ? err.message : "링크 생성 실패");
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
    try {
      const res = await fetchWithTimeout(`/api/links/${revokeTarget.id}`, {
        method: "DELETE",
        headers: getAuthHeaders(),
      });
      if (!res.ok && res.status !== 204) throw new Error(`HTTP ${res.status}`);
      setRevokeTarget(null);
      setNewLinkUrl(null);
      await fetchTempLinks();
    } catch {
      setRevokeTarget(null);
    } finally {
      setRevokeLoading(false);
    }
  };

  useEffect(() => {
    fetchContacts();
    fetchSites();
    fetchTempLinks();
    if (showAccounts) {
      fetchAccounts();
      fetchCameras();
      fetchInvitations();
      fetchStorage();
      fetchArchives();
      fetchSettings();
    }
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
      setSiteEditError(isTimeoutError(err) ? timeoutMessage() : err instanceof Error ? err.message : "수정 실패");
    } finally {
      setSiteEditLoading(false);
    }
  };

  const handleAdd = async () => {
    setAddError(null);
    if (!addName.trim()) {
      setAddError("이름을 입력하세요");
      return;
    }
    if (!PHONE_REGEX.test(addPhone)) {
      setAddError("전화번호 형식이 올바르지 않습니다 (예: 010-1234-5678)");
      return;
    }
    setAddLoading(true);
    try {
      const res = await fetchWithTimeout("/api/contacts", {
        method: "POST",
        headers: getAuthHeaders(),
        body: JSON.stringify({ name: addName.trim(), phone: addPhone, email: addEmail.trim(), notifyEmail: addNotifyEmail }),
      });
      if (!res.ok) {
        const body = await res.json().catch(() => null);
        throw new Error(body?.error || `HTTP ${res.status}`);
      }
      setAddName("");
      setAddPhone("");
      setAddEmail("");
      setAddNotifyEmail(false);
      setShowAddForm(false);
      await fetchContacts();
    } catch (err) {
      setAddError(isTimeoutError(err) ? timeoutMessage() : err instanceof Error ? err.message : "추가 실패");
    } finally {
      setAddLoading(false);
    }
  };

  const startEdit = (contact: Contact) => {
    setEditId(contact.id);
    setEditName(contact.name);
    setEditPhone(contact.phone);
    setEditEmail(contact.email || "");
    setEditNotifyEmail(contact.notifyEmail);
    setEditError(null);
  };

  const cancelEdit = () => {
    setEditId(null);
    setEditError(null);
  };

  const handleEdit = async () => {
    if (editId === null) return;
    setEditError(null);
    if (!editName.trim()) {
      setEditError("이름을 입력하세요");
      return;
    }
    if (!PHONE_REGEX.test(editPhone)) {
      setEditError("전화번호 형식이 올바르지 않습니다");
      return;
    }
    setEditLoading(true);
    try {
      const res = await fetchWithTimeout(`/api/contacts/${editId}`, {
        method: "PUT",
        headers: getAuthHeaders(),
        body: JSON.stringify({ name: editName.trim(), phone: editPhone, email: editEmail.trim(), notifyEmail: editNotifyEmail }),
      });
      if (!res.ok) {
        const body = await res.json().catch(() => null);
        throw new Error(body?.error || `HTTP ${res.status}`);
      }
      setEditId(null);
      await fetchContacts();
    } catch (err) {
      setEditError(isTimeoutError(err) ? timeoutMessage() : err instanceof Error ? err.message : "수정 실패");
    } finally {
      setEditLoading(false);
    }
  };

  const handleDelete = async () => {
    if (!deleteTarget) return;
    setDeleteLoading(true);
    try {
      const res = await fetchWithTimeout(`/api/contacts/${deleteTarget.id}`, {
        method: "DELETE",
        headers: getAuthHeaders(),
      });
      if (!res.ok) throw new Error(`HTTP ${res.status}`);
      setDeleteTarget(null);
      await fetchContacts();
    } catch {
      setDeleteTarget(null);
    } finally {
      setDeleteLoading(false);
    }
  };

  if (loading) {
    return (
      <div className="page">
        <h2>연락처 관리</h2>
        <p className="mgmt-loading">로딩 중...</p>
      </div>
    );
  }

  if (error) {
    return (
      <div className="page">
        <h2>연락처 관리</h2>
        <p className="mgmt-error">{error}</p>
      </div>
    );
  }

  return (
    <div className="page">
      {/* Unified system health (services + sensors) — top of management page */}
      <HealthPanel />
      <div className="mgmt-section-divider" />

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

      {/* System settings section (admin only) */}
      {showAccounts && (
        <>
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
          <div className="mgmt-section-divider" />
        </>
      )}

      {/* Temp links section */}
      <div className="mgmt-header">
        <h2>임시 CCTV 링크</h2>
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
                  생성: {new Date(link.createdAt).toLocaleString("ko-KR")}
                </span>
                <span className="mgmt-card-phone">
                  만료: {new Date(link.expiresAt).toLocaleString("ko-KR")}
                </span>
              </div>
              <div className="mgmt-card-actions">
                <button
                  className="mgmt-btn mgmt-btn-small mgmt-btn-danger"
                  onClick={() => setRevokeTarget(link)}
                >
                  폐기
                </button>
              </div>
            </div>
          ))}
        </div>
      )}

      <div className="mgmt-section-divider" />

      {/* Contacts section */}
      <div className="mgmt-header">
        <h2>연락처 관리</h2>
        {!showAddForm && (
          <button
            className="mgmt-btn mgmt-btn-primary"
            onClick={() => {
              setShowAddForm(true);
              setAddError(null);
            }}
          >
            + 추가
          </button>
        )}
      </div>

      {/* Add form */}
      {showAddForm && (
        <div className="mgmt-form">
          <div className="mgmt-form-field">
            <label>이름</label>
            <input
              type="text"
              value={addName}
              onChange={(e) => setAddName(e.target.value)}
              placeholder="홍길동"
              autoFocus
            />
          </div>
          <div className="mgmt-form-field">
            <label>전화번호</label>
            <input
              type="tel"
              value={addPhone}
              onChange={(e) => setAddPhone(formatPhoneInput(e.target.value))}
              placeholder="010-1234-5678"
              maxLength={13}
            />
          </div>
          <div className="mgmt-form-field">
            <label>이메일</label>
            <input
              type="email"
              value={addEmail}
              onChange={(e) => setAddEmail(e.target.value)}
              placeholder="example@email.com"
            />
          </div>
          <div className="mgmt-form-field mgmt-form-checkbox">
            <label>
              <input
                type="checkbox"
                checked={addNotifyEmail}
                onChange={(e) => setAddNotifyEmail(e.target.checked)}
              />
              이메일 알림 수신
            </label>
          </div>
          {addError && <p className="mgmt-form-error">{addError}</p>}
          <div className="mgmt-form-actions">
            <button
              className="mgmt-btn mgmt-btn-primary"
              onClick={handleAdd}
              disabled={addLoading}
            >
              {addLoading ? "저장 중..." : "저장"}
            </button>
            <button
              className="mgmt-btn mgmt-btn-secondary"
              onClick={() => {
                setShowAddForm(false);
                setAddName("");
                setAddPhone("");
                setAddEmail("");
                setAddNotifyEmail(false);
                setAddError(null);
              }}
            >
              취소
            </button>
          </div>
        </div>
      )}

      {/* Contact list */}
      {contacts.length === 0 ? (
        <p className="mgmt-empty">등록된 연락처가 없습니다</p>
      ) : (
        <div className="mgmt-list">
          {contacts.map((contact) =>
            editId === contact.id ? (
              <div key={contact.id} className="mgmt-card mgmt-card-editing">
                <div className="mgmt-form-field">
                  <label>이름</label>
                  <input
                    type="text"
                    value={editName}
                    onChange={(e) => setEditName(e.target.value)}
                    autoFocus
                  />
                </div>
                <div className="mgmt-form-field">
                  <label>전화번호</label>
                  <input
                    type="tel"
                    value={editPhone}
                    onChange={(e) =>
                      setEditPhone(formatPhoneInput(e.target.value))
                    }
                    maxLength={13}
                  />
                </div>
                <div className="mgmt-form-field">
                  <label>이메일</label>
                  <input
                    type="email"
                    value={editEmail}
                    onChange={(e) => setEditEmail(e.target.value)}
                    placeholder="example@email.com"
                  />
                </div>
                <div className="mgmt-form-field mgmt-form-checkbox">
                  <label>
                    <input
                      type="checkbox"
                      checked={editNotifyEmail}
                      onChange={(e) => setEditNotifyEmail(e.target.checked)}
                    />
                    이메일 알림 수신
                  </label>
                </div>
                {editError && <p className="mgmt-form-error">{editError}</p>}
                <div className="mgmt-form-actions">
                  <button
                    className="mgmt-btn mgmt-btn-primary"
                    onClick={handleEdit}
                    disabled={editLoading}
                  >
                    {editLoading ? "저장 중..." : "저장"}
                  </button>
                  <button
                    className="mgmt-btn mgmt-btn-secondary"
                    onClick={cancelEdit}
                  >
                    취소
                  </button>
                </div>
              </div>
            ) : (
              <div key={contact.id} className="mgmt-card">
                <div className="mgmt-card-info">
                  <span className="mgmt-card-name">{contact.name}</span>
                  <span className="mgmt-card-phone">{contact.phone}</span>
                  {contact.email && <span className="mgmt-card-email">{contact.email}</span>}
                  {contact.notifyEmail && <span className="mgmt-card-badge">이메일 알림</span>}
                </div>
                <div className="mgmt-card-actions">
                  <button
                    className="mgmt-btn mgmt-btn-small"
                    onClick={() => startEdit(contact)}
                  >
                    수정
                  </button>
                  <button
                    className="mgmt-btn mgmt-btn-small mgmt-btn-danger"
                    onClick={() => setDeleteTarget(contact)}
                  >
                    삭제
                  </button>
                </div>
              </div>
            )
          )}
        </div>
      )}

      {/* Device management section */}
      <div className="mgmt-section-divider" />
      <DevicesSection />

      {/* Camera management section (admin only) */}
      {showAccounts && (
        <>
          <div className="mgmt-section-divider" />
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
        </>
      )}

      {/* Account management section (admin only) */}
      {showAccounts && (
        <>
          <div className="mgmt-section-divider" />
          <div className="mgmt-header">
            <h2>계정 관리</h2>
          </div>

          {accountsLoading ? (
            <p className="mgmt-loading">로딩 중...</p>
          ) : accountsError ? (
            <p className="mgmt-error">{accountsError}</p>
          ) : (
            <>
              {/* Pending users */}
              <h3 className="mgmt-sub-header">승인 대기</h3>
              {pendingUsers.length === 0 ? (
                <p className="mgmt-empty">대기 중인 가입 요청이 없습니다</p>
              ) : (
                <div className="mgmt-list">
                  {pendingUsers.map((user) => (
                    <div key={user.id} className="mgmt-card">
                      <div className="mgmt-card-info">
                        <span className="mgmt-card-name">{user.name}</span>
                        <span className="mgmt-card-phone">@{user.username}</span>
                        <span className="mgmt-card-phone">
                          {new Date(user.createdAt).toLocaleDateString("ko-KR")} 가입 요청
                        </span>
                      </div>
                      <div className="mgmt-card-actions">
                        <button
                          className="mgmt-btn mgmt-btn-small mgmt-btn-primary"
                          onClick={() => handleApprove(user.id)}
                          disabled={approveLoading === user.id}
                        >
                          {approveLoading === user.id ? "승인 중..." : "승인"}
                        </button>
                        <button
                          className="mgmt-btn mgmt-btn-small mgmt-btn-danger"
                          onClick={() => handleReject(user.id)}
                          disabled={rejectLoading === user.id}
                        >
                          {rejectLoading === user.id ? "거절 중..." : "거절"}
                        </button>
                      </div>
                    </div>
                  ))}
                </div>
              )}

              {/* Active users */}
              <h3 className="mgmt-sub-header">활성 사용자</h3>
              {activeUsers.length === 0 ? (
                <p className="mgmt-empty">활성 사용자가 없습니다</p>
              ) : (
                <div className="mgmt-list">
                  {activeUsers.map((user) => (
                    <div key={user.id} className="mgmt-card">
                      <div className="mgmt-card-info">
                        <span className="mgmt-card-name">
                          {user.name}
                          {user.role === "admin" && (
                            <span className="mgmt-badge-admin">관리자</span>
                          )}
                        </span>
                        <span className="mgmt-card-phone">@{user.username}</span>
                      </div>
                    </div>
                  ))}
                </div>
              )}
            </>
          )}
        </>
      )}

      {/* Invitation management section (admin only) */}
      {showAccounts && (
        <>
          <div className="mgmt-section-divider" />
          <div className="mgmt-header">
            <h2>초대 관리</h2>
          </div>

          {/* Invite form */}
          <div className="mgmt-form">
            <div className="mgmt-form-field">
              <label>이메일</label>
              <input
                type="email"
                value={inviteEmail}
                onChange={(e) => setInviteEmail(e.target.value)}
                placeholder="초대할 이메일 주소"
                onKeyDown={(e) => { if (e.key === "Enter") handleSendInvite(); }}
              />
            </div>
            {inviteError && <p className="mgmt-form-error">{inviteError}</p>}
            {inviteSuccess && <p className="mgmt-form-success">{inviteSuccess}</p>}
            <div className="mgmt-form-actions">
              <button
                className="mgmt-btn mgmt-btn-primary"
                onClick={handleSendInvite}
                disabled={inviteLoading}
              >
                {inviteLoading ? "발송 중..." : "초대 발송"}
              </button>
            </div>
          </div>

          {/* Invitation list */}
          {invitationsLoading ? (
            <p className="mgmt-loading">로딩 중...</p>
          ) : invitationsError ? (
            <p className="mgmt-error">{invitationsError}</p>
          ) : invitations.length === 0 ? (
            <p className="mgmt-empty">발송된 초대가 없습니다</p>
          ) : (
            <div className="mgmt-list">
              {invitations.map((inv) => (
                <div key={inv.id} className="mgmt-card">
                  <div className="mgmt-card-info">
                    <span className="mgmt-card-name">
                      {inv.email}
                      <span className={`mgmt-badge-invite mgmt-badge-invite-${inv.status}`}>
                        {inv.status === "pending" ? "대기" : inv.status === "accepted" ? "수락" : inv.status === "expired" ? "만료" : "취소"}
                      </span>
                    </span>
                    <span className="mgmt-card-phone">
                      {new Date(inv.createdAt + "Z").toLocaleDateString("ko-KR")} 발송
                    </span>
                  </div>
                  <div className="mgmt-card-actions">
                    {inv.status === "pending" && (
                      <button
                        className="mgmt-btn mgmt-btn-small mgmt-btn-danger"
                        onClick={() => setCancelInviteTarget(inv)}
                      >
                        취소
                      </button>
                    )}
                  </div>
                </div>
              ))}
            </div>
          )}
        </>
      )}

      {/* Test alert simulation section (admin only) */}
      {showAccounts && (
        <>
          <div className="mgmt-section-divider" />
          <div className="mgmt-header">
            <h2>비상 신호 시뮬레이션</h2>
          </div>
          <div className="mgmt-test-alert-section">
            <p className="mgmt-test-alert-desc">
              테스트 비상 신호를 발송하여 전체 알림 체인(MQTT → hw-gateway → notifier → KakaoTalk/SMS/이메일)을 검증합니다.
              모든 메시지에 [테스트] 접두사가 포함됩니다.
            </p>
            {testAlertError && <p className="mgmt-form-error">{testAlertError}</p>}
            {testAlertSuccess && <p className="mgmt-form-success">{testAlertSuccess}</p>}
            <div className="mgmt-form-actions">
              <button
                className="mgmt-btn mgmt-btn-warning"
                onClick={handleTestAlert}
                disabled={testAlertLoading}
              >
                {testAlertLoading ? "발송 중..." : "비상 신호 시뮬레이션"}
              </button>
            </div>
          </div>
        </>
      )}

      {/* Storage & Archives section (admin only) */}
      {showAccounts && (
        <>
          <div className="mgmt-section-divider" />
          <div className="mgmt-header">
            <h2>저장소 관리</h2>
          </div>

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
                            {new Date(firstArchive.from).toLocaleString("ko-KR")} ~ {new Date(firstArchive.to).toLocaleString("ko-KR")}
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
                          </div>
                        ))}
                      </div>
                    </div>
                  );
                })}
              </div>
            );
          })()}
        </>
      )}

      {/* Cancel invitation confirmation dialog */}
      {cancelInviteTarget && (
        <div className="mgmt-modal-overlay" onClick={() => setCancelInviteTarget(null)}>
          <div className="mgmt-modal" onClick={(e) => e.stopPropagation()}>
            <p className="mgmt-modal-text">
              <strong>{cancelInviteTarget.email}</strong> 초대를 취소하시겠습니까?
            </p>
            <div className="mgmt-form-actions">
              <button
                className="mgmt-btn mgmt-btn-danger"
                onClick={handleCancelInvite}
                disabled={cancelInviteLoading}
              >
                {cancelInviteLoading ? "취소 중..." : "초대 취소"}
              </button>
              <button
                className="mgmt-btn mgmt-btn-secondary"
                onClick={() => setCancelInviteTarget(null)}
              >
                닫기
              </button>
            </div>
          </div>
        </div>
      )}

      {/* Revoke confirmation dialog */}
      {revokeTarget && (
        <div className="mgmt-modal-overlay" onClick={() => setRevokeTarget(null)}>
          <div className="mgmt-modal" onClick={(e) => e.stopPropagation()}>
            <p className="mgmt-modal-text">
              이 임시 링크를 폐기하시겠습니까?<br />
              <small>폐기 후에는 해당 링크로 접속할 수 없습니다.</small>
            </p>
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
                onClick={() => setRevokeTarget(null)}
              >
                취소
              </button>
            </div>
          </div>
        </div>
      )}

      {/* Camera delete confirmation dialog */}
      {camDeleteTarget && (
        <div className="mgmt-modal-overlay" onClick={() => setCamDeleteTarget(null)}>
          <div className="mgmt-modal" onClick={(e) => e.stopPropagation()}>
            <p className="mgmt-modal-text">
              <strong>{camDeleteTarget.name}</strong> 카메라를 삭제하시겠습니까?
            </p>
            <div className="mgmt-form-actions">
              <button className="mgmt-btn mgmt-btn-danger" onClick={handleCameraDelete} disabled={camDeleteLoading}>
                {camDeleteLoading ? "삭제 중..." : "삭제"}
              </button>
              <button className="mgmt-btn mgmt-btn-secondary" onClick={() => setCamDeleteTarget(null)}>취소</button>
            </div>
          </div>
        </div>
      )}

      {/* Archive delete confirmation dialog */}
      {archiveDeleteTarget && (
        <div className="mgmt-modal-overlay" onClick={() => setArchiveDeleteTarget(null)}>
          <div className="mgmt-modal" onClick={(e) => e.stopPropagation()}>
            <p className="mgmt-modal-text">
              <strong>{archiveDeleteTarget.streamKey}</strong> 보관 영상을 삭제하시겠습니까?<br />
              <small>{archiveDeleteTarget.sizeBytes > 0 ? formatBytes(archiveDeleteTarget.sizeBytes) : ""} 디스크 공간이 확보됩니다.</small>
            </p>
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
                onClick={() => setArchiveDeleteTarget(null)}
              >
                취소
              </button>
            </div>
          </div>
        </div>
      )}

      {/* Incident archive delete confirmation dialog */}
      {incidentDeleteTarget && (
        <div className="mgmt-modal-overlay" onClick={() => setIncidentDeleteTarget(null)}>
          <div className="mgmt-modal" onClick={(e) => e.stopPropagation()}>
            <p className="mgmt-modal-text">
              <strong>{incidentDeleteTarget}</strong> 사건의 모든 보관 영상을 삭제하시겠습니까?<br />
              <small>해당 사건의 모든 카메라 영상이 삭제됩니다.</small>
            </p>
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
                onClick={() => setIncidentDeleteTarget(null)}
              >
                취소
              </button>
            </div>
          </div>
        </div>
      )}

      {/* Delete confirmation dialog */}
      {deleteTarget && (
        <div className="mgmt-modal-overlay" onClick={() => setDeleteTarget(null)}>
          <div className="mgmt-modal" onClick={(e) => e.stopPropagation()}>
            <p className="mgmt-modal-text">
              <strong>{deleteTarget.name}</strong> 연락처를 삭제하시겠습니까?
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
    </div>
  );
}
