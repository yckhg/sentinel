import { useEffect, useState } from "react";
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

const HEALTH_KEYS = [
  { key: "health.service_check_interval_sec", label: "서비스 점검 주기(초)", fallback: "30" },
  { key: "health.service_down_threshold_sec", label: "서비스 Down 임계값(초)", fallback: "90" },
  { key: "health.sensor_alive_threshold_sec", label: "센서 생존 임계값(초)", fallback: "60" },
];

export default function SettingsPage({ onLogout }: SettingsPageProps) {
  const [currentPassword, setCurrentPassword] = useState("");
  const [newPassword, setNewPassword] = useState("");
  const [confirmPassword, setConfirmPassword] = useState("");
  const [pwLoading, setPwLoading] = useState(false);
  const [pwError, setPwError] = useState<string | null>(null);
  const [pwSuccess, setPwSuccess] = useState<string | null>(null);

  const admin = isAdmin();
  const [healthValues, setHealthValues] = useState<Record<string, string>>({});
  const [healthLoading, setHealthLoading] = useState(false);
  const [healthSaving, setHealthSaving] = useState(false);
  const [healthError, setHealthError] = useState<string | null>(null);
  const [healthSuccess, setHealthSuccess] = useState<string | null>(null);

  useEffect(() => {
    if (!admin) return;
    let cancelled = false;
    (async () => {
      setHealthLoading(true);
      try {
        const res = await fetchWithTimeout("/api/settings", { headers: getAuthHeaders() });
        if (!res.ok) throw new Error(`HTTP ${res.status}`);
        const data: { key: string; value: string }[] = await res.json();
        if (cancelled) return;
        const map: Record<string, string> = {};
        for (const k of HEALTH_KEYS) {
          const row = data.find((r) => r.key === k.key);
          map[k.key] = row?.value || k.fallback;
        }
        setHealthValues(map);
      } catch (err) {
        if (!cancelled) {
          setHealthError(
            isTimeoutError(err)
              ? timeoutMessage()
              : err instanceof Error
                ? err.message
                : "설정을 불러올 수 없습니다",
          );
        }
      } finally {
        if (!cancelled) setHealthLoading(false);
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [admin]);

  const handleHealthSave = async () => {
    setHealthError(null);
    setHealthSuccess(null);
    for (const k of HEALTH_KEYS) {
      const v = healthValues[k.key];
      const n = Number(v);
      if (!v || !Number.isFinite(n) || n <= 0) {
        setHealthError(`${k.label}: 양의 정수를 입력해 주세요`);
        return;
      }
    }
    setHealthSaving(true);
    try {
      for (const k of HEALTH_KEYS) {
        const res = await fetchWithTimeout(`/api/settings/${encodeURIComponent(k.key)}`, {
          method: "PUT",
          headers: getAuthHeaders(),
          body: JSON.stringify({ value: String(Number(healthValues[k.key])) }),
        });
        if (!res.ok) {
          const body = await res.json().catch(() => null);
          throw new Error(body?.error || `HTTP ${res.status}`);
        }
      }
      setHealthSuccess("저장되었습니다");
    } catch (err) {
      setHealthError(
        isTimeoutError(err)
          ? timeoutMessage()
          : err instanceof Error
            ? err.message
            : "저장 실패",
      );
    } finally {
      setHealthSaving(false);
    }
  };

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

      {admin && (
        <>
          <div className="mgmt-section-divider">Health 임계값</div>
          <div className="mgmt-form" style={{ marginBottom: "2rem" }}>
            {healthLoading ? (
              <p className="mgmt-loading">로딩 중...</p>
            ) : (
              <>
                {HEALTH_KEYS.map((k) => (
                  <div className="mgmt-form-field" key={k.key}>
                    <label>{k.label}</label>
                    <input
                      type="number"
                      min={1}
                      value={healthValues[k.key] ?? ""}
                      onChange={(e) =>
                        setHealthValues((prev) => ({ ...prev, [k.key]: e.target.value }))
                      }
                      disabled={healthSaving}
                    />
                  </div>
                ))}
                {healthError && <div className="mgmt-form-error">{healthError}</div>}
                {healthSuccess && <div className="mgmt-form-success">{healthSuccess}</div>}
                <div className="mgmt-form-actions">
                  <button
                    className="mgmt-btn mgmt-btn-primary"
                    onClick={handleHealthSave}
                    disabled={healthSaving}
                  >
                    {healthSaving ? "저장 중..." : "저장"}
                  </button>
                </div>
              </>
            )}
          </div>
        </>
      )}

      {/* Logout Section */}
      <div className="mgmt-section-divider">계정</div>
      <button className="mgmt-btn mgmt-btn-danger" onClick={onLogout}>
        로그아웃
      </button>
    </div>
  );
}
