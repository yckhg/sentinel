import { useState } from "react";
import { fetchWithTimeout, isTimeoutError, timeoutMessage } from "../utils/fetchWithTimeout";

interface SettingsPageProps {
  onLogout: () => void;
}

function getAuthHeaders(): HeadersInit {
  const token = localStorage.getItem("token");
  return token
    ? { Authorization: `Bearer ${token}`, "Content-Type": "application/json" }
    : { "Content-Type": "application/json" };
}

export default function SettingsPage({ onLogout }: SettingsPageProps) {
  const [currentPassword, setCurrentPassword] = useState("");
  const [newPassword, setNewPassword] = useState("");
  const [confirmPassword, setConfirmPassword] = useState("");
  const [pwLoading, setPwLoading] = useState(false);
  const [pwError, setPwError] = useState<string | null>(null);
  const [pwSuccess, setPwSuccess] = useState<string | null>(null);

  const handleChangePassword = async () => {
    setPwError(null);
    setPwSuccess(null);

    if (!currentPassword || !newPassword || !confirmPassword) {
      setPwError("모든 필드를 입력해 주세요.");
      return;
    }
    if (newPassword.length < 8) {
      setPwError("새 비밀번호는 8자 이상이어야 합니다.");
      return;
    }
    if (newPassword !== confirmPassword) {
      setPwError("새 비밀번호가 일치하지 않습니다.");
      return;
    }

    setPwLoading(true);
    try {
      const res = await fetchWithTimeout("/api/auth/change-password", {
        method: "POST",
        headers: getAuthHeaders(),
        body: JSON.stringify({ currentPassword, newPassword }),
      });
      if (!res.ok) {
        const body = await res.json().catch(() => null);
        throw new Error(body?.error || `HTTP ${res.status}`);
      }
      setPwSuccess("비밀번호가 변경되었습니다.");
      setCurrentPassword("");
      setNewPassword("");
      setConfirmPassword("");
    } catch (err) {
      setPwError(
        isTimeoutError(err)
          ? timeoutMessage()
          : err instanceof Error
            ? err.message
            : "비밀번호 변경에 실패했습니다."
      );
    } finally {
      setPwLoading(false);
    }
  };

  return (
    <div className="page">
      <h2>설정</h2>
      <p style={{ color: "#666", marginBottom: "1.5rem" }}>계정 및 시스템 설정</p>

      {/* Password Change Section */}
      <div className="mgmt-section-divider">비밀번호 변경</div>
      <div className="mgmt-form" style={{ marginBottom: "2rem" }}>
        <div className="mgmt-form-field">
          <label>현재 비밀번호</label>
          <input
            type="password"
            value={currentPassword}
            onChange={(e) => setCurrentPassword(e.target.value)}
            placeholder="현재 비밀번호"
            disabled={pwLoading}
          />
        </div>
        <div className="mgmt-form-field">
          <label>새 비밀번호</label>
          <input
            type="password"
            value={newPassword}
            onChange={(e) => setNewPassword(e.target.value)}
            placeholder="8자 이상"
            disabled={pwLoading}
          />
        </div>
        <div className="mgmt-form-field">
          <label>새 비밀번호 확인</label>
          <input
            type="password"
            value={confirmPassword}
            onChange={(e) => setConfirmPassword(e.target.value)}
            placeholder="새 비밀번호 확인"
            disabled={pwLoading}
          />
        </div>
        {pwError && <div className="mgmt-form-error">{pwError}</div>}
        {pwSuccess && <div className="mgmt-form-success">{pwSuccess}</div>}
        <div className="mgmt-form-actions">
          <button
            className="mgmt-btn mgmt-btn-primary"
            onClick={handleChangePassword}
            disabled={pwLoading}
          >
            {pwLoading ? "변경 중..." : "비밀번호 변경"}
          </button>
        </div>
      </div>

      {/* Logout Section */}
      <div className="mgmt-section-divider">계정</div>
      <button className="mgmt-btn mgmt-btn-danger" onClick={onLogout}>
        로그아웃
      </button>
    </div>
  );
}
