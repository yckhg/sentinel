import { useEffect, useState } from "react";
import { navigate } from "../../utils/navigation";
import { fetchWithTimeout, isTimeoutError, timeoutMessage } from "../../utils/fetchWithTimeout";
import { formatKstDate } from "../../utils/datetime";
import Modal from "../../components/Modal";

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

function getAuthHeaders(): HeadersInit {
  const token = localStorage.getItem("token");
  return token
    ? { Authorization: `Bearer ${token}`, "Content-Type": "application/json" }
    : { "Content-Type": "application/json" };
}

// Self-contained relocation of the former Management page's 계정 관리 + 초대 관리
// sections into the /admin/users subpage. Behavior is preserved verbatim; the
// only structural change is the admin-only mount (the previous `showAccounts`
// guard is dropped because this route is admin-gated) and the admin-page shell.
export default function UsersPage() {
  // Account management state
  const [pendingUsers, setPendingUsers] = useState<PendingUser[]>([]);
  const [activeUsers, setActiveUsers] = useState<ActiveUser[]>([]);
  const [accountsLoading, setAccountsLoading] = useState(true);
  const [accountsError, setAccountsError] = useState<string | null>(null);
  const [approveLoading, setApproveLoading] = useState<number | null>(null);
  const [rejectLoading, setRejectLoading] = useState<number | null>(null);

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

  // Shared inline error for the cancel confirm modal. Previously delete
  // failures were silently swallowed (modal just closed) so the user assumed
  // success (#103).
  const [actionError, setActionError] = useState<string | null>(null);

  const errorMessage = (err: unknown): string =>
    isTimeoutError(err)
      ? timeoutMessage()
      : err instanceof Error
        ? err.message
        : "요청을 처리하지 못했습니다";

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
    setActionError(null);
    try {
      const res = await fetchWithTimeout(`/api/invitations/${cancelInviteTarget.id}`, {
        method: "DELETE",
        headers: getAuthHeaders(),
      });
      if (!res.ok && res.status !== 204) throw new Error(`HTTP ${res.status}`);
      setCancelInviteTarget(null);
      await fetchInvitations();
    } catch (err) {
      setActionError(errorMessage(err)); // keep modal open, surface failure (#103)
    } finally {
      setCancelInviteLoading(false);
    }
  };

  useEffect(() => {
    fetchAccounts();
    fetchInvitations();
  }, []);

  return (
    <div className="admin-page" data-testid="admin-page" data-slug="users">
      <button
        type="button"
        className="admin-back"
        data-testid="admin-back"
        onClick={() => navigate("/admin")}
      >
        ← 관리
      </button>
      <h1 className="admin-page-title">사용자(계정+초대)</h1>

      {/* Account management section */}
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
                      {formatKstDate(user.createdAt)} 가입 요청
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

      {/* Invitation management section */}
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
                  {formatKstDate(inv.createdAt)} 발송
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

      {/* Cancel invitation confirmation dialog */}
      {cancelInviteTarget && (
        <Modal
          onClose={() => { setCancelInviteTarget(null); setActionError(null); }}
          ariaLabel="초대 취소 확인"
        >
          <p className="mgmt-modal-text">
            <strong>{cancelInviteTarget.email}</strong> 초대를 취소하시겠습니까?
          </p>
          {actionError && <p className="mgmt-form-error">{actionError}</p>}
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
              onClick={() => { setCancelInviteTarget(null); setActionError(null); }}
            >
              닫기
            </button>
          </div>
        </Modal>
      )}
    </div>
  );
}
