import { useEffect, useState } from "react";
import { navigate } from "../../utils/navigation";
import { fetchWithTimeout, isTimeoutError, timeoutMessage } from "../../utils/fetchWithTimeout";

// Relocated from ManagementPage "알림 채널 테스트 발송" section (admin-IA leaf
// page-notify-test). Behavior-preserving move: same endpoints, same request/
// response contract, same honest outcome reporting. Canonical contracts:
// docs/spec/admin-page-notify-test.md + docs/spec/notification-test-send.md.
// The admin-only access gate is inherited from the routing contract (App.tsx),
// not re-defined here.

interface ChannelUsability {
  usable: boolean;
  reason?: string;
}

interface ChannelStatus {
  email: ChannelUsability;
  sms: ChannelUsability;
}

interface NotifyTestResult {
  msg: string;
  error: boolean;
}

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

export default function NotifyTestPage() {
  // Notification channel test-send (docs/spec/notification-test-send.md §출력 13).
  // 요청 시점 설정 판정: GET /api/notifications/channels 로 채널 usable 상태를 읽어
  // 미설정(usable=false) 채널은 안내 + 버튼 비활성. 발송은 POST /api/notifications/test.
  const [channelStatus, setChannelStatus] = useState<ChannelStatus | null>(null);
  const [notifyTestEmail, setNotifyTestEmail] = useState("");
  const [notifyTestPhone, setNotifyTestPhone] = useState("");
  const [notifyTestEmailLoading, setNotifyTestEmailLoading] = useState(false);
  const [notifyTestSmsLoading, setNotifyTestSmsLoading] = useState(false);
  const [notifyTestEmailResult, setNotifyTestEmailResult] = useState<NotifyTestResult | null>(null);
  const [notifyTestSmsResult, setNotifyTestSmsResult] = useState<NotifyTestResult | null>(null);

  const fetchChannelStatus = async () => {
    try {
      const res = await fetchWithTimeout("/api/notifications/channels", { headers: getAuthHeaders() });
      if (!res.ok) throw new Error(`HTTP ${res.status}`);
      const data: ChannelStatus = await res.json();
      setChannelStatus(data);
    } catch {
      // notifier 미도달/조회 실패는 usability 미상 — null 유지(§출력 14, 거짓 미설정 강등 없음).
      setChannelStatus(null);
    }
  };

  useEffect(() => {
    fetchChannelStatus();
  }, []);

  // 채널별 단건 테스트 발송. 관리자가 그 자리에서 입력한 명시 단일 대상에게만 보낸다
  // (등록 연락처 팬아웃 없음). outcome(sent/failed/not_configured)을 정직하게 표시한다.
  const handleNotifyTest = async (channel: "email" | "sms", target: string) => {
    const setLoading = channel === "email" ? setNotifyTestEmailLoading : setNotifyTestSmsLoading;
    const setResult = channel === "email" ? setNotifyTestEmailResult : setNotifyTestSmsResult;
    setLoading(true);
    setResult(null);
    try {
      const res = await fetchWithTimeout("/api/notifications/test", {
        method: "POST",
        headers: getAuthHeaders(),
        body: JSON.stringify({ channel, target }),
      });
      const data = await res.json().catch(() => ({} as { outcome?: string; reason?: string; error?: string }));
      if (!res.ok) {
        const msg =
          res.status === 400 ? "입력값이 올바르지 않습니다"
          : res.status === 429 ? "잠시 후 다시 시도하세요 (분당 1건 제한)"
          : res.status === 502 ? "발송 서비스에 연결할 수 없습니다"
          : data.error || `발송 실패 (HTTP ${res.status})`;
        setResult({ msg, error: true });
        return;
      }
      const outcome = (data as { outcome?: string; reason?: string }).outcome;
      if (outcome === "sent") {
        setResult({ msg: "테스트 메시지를 발송했습니다", error: false });
      } else if (outcome === "not_configured") {
        setResult({ msg: "채널이 미설정 상태입니다", error: true });
      } else if (outcome === "failed") {
        const reason = (data as { reason?: string }).reason;
        setResult({ msg: reason ? `발송 실패: ${reason}` : "발송에 실패했습니다", error: true });
      } else {
        setResult({ msg: "발송 결과를 확인할 수 없습니다", error: true });
      }
    } catch (err) {
      setResult({ msg: isTimeoutError(err) ? timeoutMessage() : "발송 요청을 처리하지 못했습니다", error: true });
    } finally {
      setLoading(false);
    }
  };

  return (
    <div className="admin-page" data-testid="admin-page" data-slug="notify-test">
      <button
        type="button"
        className="admin-back"
        data-testid="admin-back"
        onClick={() => navigate("/admin")}
      >
        ← 관리
      </button>
      <h1 className="admin-page-title">알림 채널 테스트 발송</h1>

      {/* Notification channel test-send — spec notification-test-send §출력 13 */}
      <div data-testid="notify-test">
        <div className="mgmt-form">
          <p className="mgmt-form-hint">
            입력한 단일 대상에게 채널별로 테스트 메시지 1건을 보내 실제 발송 동작을 확인합니다.
            등록된 비상연락처로 발송되지 않습니다.
          </p>

          {/* 이메일 채널 */}
          <div className="mgmt-form-field">
            <label>이메일 테스트 대상</label>
            <input
              type="email"
              value={notifyTestEmail}
              onChange={(e) => { setNotifyTestEmail(e.target.value); setNotifyTestEmailResult(null); }}
              placeholder="test@example.com"
            />
            {channelStatus && !channelStatus.email.usable && (
              <span className="mgmt-form-hint">이메일 채널 미설정 — 테스트를 보낼 수 없습니다</span>
            )}
          </div>
          {notifyTestEmailResult && (
            <p className={notifyTestEmailResult.error ? "mgmt-form-error" : "mgmt-form-success"}>
              {notifyTestEmailResult.msg}
            </p>
          )}
          <div className="mgmt-form-actions">
            <button
              className="mgmt-btn mgmt-btn-primary"
              onClick={() => handleNotifyTest("email", notifyTestEmail.trim())}
              disabled={notifyTestEmailLoading || (channelStatus ? !channelStatus.email.usable : false)}
            >
              {notifyTestEmailLoading ? "발송 중..." : "이메일 테스트 전송"}
            </button>
          </div>

          {/* SMS 채널 */}
          <div className="mgmt-form-field">
            <label>SMS 테스트 대상</label>
            <input
              type="tel"
              value={notifyTestPhone}
              onChange={(e) => { setNotifyTestPhone(formatPhoneInput(e.target.value)); setNotifyTestSmsResult(null); }}
              placeholder="010-1234-5678"
              maxLength={13}
            />
            {channelStatus && !channelStatus.sms.usable && (
              <span className="mgmt-form-hint">SMS 채널 미설정 — 테스트를 보낼 수 없습니다</span>
            )}
          </div>
          {notifyTestSmsResult && (
            <p className={notifyTestSmsResult.error ? "mgmt-form-error" : "mgmt-form-success"}>
              {notifyTestSmsResult.msg}
            </p>
          )}
          <div className="mgmt-form-actions">
            <button
              className="mgmt-btn mgmt-btn-primary"
              onClick={() => handleNotifyTest("sms", notifyTestPhone.trim())}
              disabled={notifyTestSmsLoading || (channelStatus ? !channelStatus.sms.usable : false)}
            >
              {notifyTestSmsLoading ? "발송 중..." : "SMS 테스트 전송"}
            </button>
          </div>
        </div>
      </div>
    </div>
  );
}
