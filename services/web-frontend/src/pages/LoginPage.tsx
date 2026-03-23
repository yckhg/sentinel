import { useState, useEffect } from "react";
import { fetchWithTimeout, isTimeoutError, timeoutMessage } from "../utils/fetchWithTimeout";

type Mode = "login" | "register";

function getInviteToken(): string | null {
  const params = new URLSearchParams(window.location.search);
  return params.get("invite");
}

interface LoginPageProps {
  onLoginSuccess: (token: string) => void;
}

export default function LoginPage({ onLoginSuccess }: LoginPageProps) {
  const [inviteToken] = useState<string | null>(getInviteToken);
  const [inviteEmail, setInviteEmail] = useState<string | null>(null);
  const [inviteValid, setInviteValid] = useState<boolean | null>(null);
  const [mode, setMode] = useState<Mode>(inviteToken ? "register" : "login");
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [confirmPassword, setConfirmPassword] = useState("");
  const [name, setName] = useState("");
  const [error, setError] = useState("");
  const [success, setSuccess] = useState("");
  const [loading, setLoading] = useState(false);

  useEffect(() => {
    if (!inviteToken) return;
    fetchWithTimeout(`/api/invitations/verify/${inviteToken}`)
      .then(async (res) => {
        if (res.ok) {
          const data = await res.json();
          setInviteEmail(data.email);
          setInviteValid(true);
        } else {
          setInviteValid(false);
          const data = await res.json().catch(() => null);
          setError(data?.error === "invitation has expired" ? "초대 링크가 만료되었습니다" : "유효하지 않은 초대 링크입니다");
        }
      })
      .catch(() => {
        setInviteValid(false);
        setError("초대 링크를 확인할 수 없습니다");
      });
  }, [inviteToken]);

  const handleLogin = async (e: React.FormEvent) => {
    e.preventDefault();
    setError("");
    setLoading(true);
    try {
      const res = await fetchWithTimeout("/auth/login", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ username, password }),
        timeoutMs: 30_000,
      });
      const data = await res.json();
      if (!res.ok) {
        setError(data.error || "로그인에 실패했습니다");
        return;
      }
      onLoginSuccess(data.token);
    } catch (err) {
      setError(isTimeoutError(err) ? timeoutMessage() : "서버에 연결할 수 없습니다");
    } finally {
      setLoading(false);
    }
  };

  const handleRegister = async (e: React.FormEvent) => {
    e.preventDefault();
    setError("");
    setSuccess("");
    if (password.length < 8) {
      setError("비밀번호는 8자 이상이어야 합니다");
      return;
    }
    if (password !== confirmPassword) {
      setError("비밀번호가 일치하지 않습니다");
      return;
    }
    setLoading(true);
    try {
      const registerBody: Record<string, string> = { username, password, confirmPassword, name };
      if (inviteToken) registerBody.inviteToken = inviteToken;
      const res = await fetchWithTimeout("/auth/register", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(registerBody),
        timeoutMs: 30_000,
      });
      const data = await res.json();
      if (!res.ok) {
        if (res.status === 409) {
          setError("이미 사용 중인 아이디입니다");
        } else {
          setError(data.error || "등록에 실패했습니다");
        }
        return;
      }
      setSuccess(inviteToken
        ? "등록이 완료되었습니다. 바로 로그인할 수 있습니다."
        : "등록이 완료되었습니다. 관리자 승인 후 로그인할 수 있습니다."
      );
      // Clean up invite params from URL
      if (inviteToken) {
        window.history.replaceState({}, "", "/");
      }
      setUsername("");
      setPassword("");
      setConfirmPassword("");
      setName("");
    } catch (err) {
      setError(isTimeoutError(err) ? timeoutMessage() : "서버에 연결할 수 없습니다");
    } finally {
      setLoading(false);
    }
  };

  const switchMode = (newMode: Mode) => {
    setMode(newMode);
    setError("");
    setSuccess("");
  };

  return (
    <div className="login-page">
      <div className="login-card">
        <h1 className="login-title">Sentinel</h1>
        <p className="login-subtitle">산업안전 실시간 모니터링</p>

        {inviteToken && inviteValid && inviteEmail && (
          <div className="login-success">
            <strong>{inviteEmail}</strong>님, 초대를 통해 접속하셨습니다. 계정을 등록해 주세요.
          </div>
        )}

        <div className="login-tabs">
          <button
            className={`login-tab${mode === "login" ? " active" : ""}`}
            onClick={() => switchMode("login")}
          >
            로그인
          </button>
          <button
            className={`login-tab${mode === "register" ? " active" : ""}`}
            onClick={() => switchMode("register")}
          >
            회원가입
          </button>
        </div>

        {error && <div className="login-error">{error}</div>}
        {success && <div className="login-success">{success}</div>}

        <form onSubmit={mode === "login" ? handleLogin : handleRegister}>
          <div className="mgmt-form-field">
            <label>아이디</label>
            <input
              type="text"
              value={username}
              onChange={(e) => setUsername(e.target.value)}
              placeholder="아이디를 입력하세요"
              required
              autoComplete="username"
            />
          </div>
          <div className="mgmt-form-field">
            <label>비밀번호</label>
            <input
              type="password"
              value={password}
              onChange={(e) => setPassword(e.target.value)}
              placeholder={mode === "register" ? "8자 이상 입력하세요" : "비밀번호를 입력하세요"}
              required
              autoComplete={mode === "login" ? "current-password" : "new-password"}
            />
          </div>
          {mode === "register" && (
            <div className="mgmt-form-field">
              <label>비밀번호 확인</label>
              <input
                type="password"
                value={confirmPassword}
                onChange={(e) => setConfirmPassword(e.target.value)}
                placeholder="비밀번호를 다시 입력하세요"
                required
                autoComplete="new-password"
              />
              {confirmPassword && password !== confirmPassword && (
                <span className="login-field-error">비밀번호가 일치하지 않습니다</span>
              )}
            </div>
          )}
          {mode === "register" && (
            <div className="mgmt-form-field">
              <label>이름</label>
              <input
                type="text"
                value={name}
                onChange={(e) => setName(e.target.value)}
                placeholder="이름을 입력하세요"
                required
              />
            </div>
          )}
          <button
            type="submit"
            className="mgmt-btn mgmt-btn-primary login-submit"
            disabled={loading || (mode === "register" && password !== confirmPassword)}
          >
            {loading
              ? "처리 중..."
              : mode === "login"
                ? "로그인"
                : "가입 신청"}
          </button>
        </form>
      </div>
    </div>
  );
}
