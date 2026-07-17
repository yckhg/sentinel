import { useState } from "react";
import { navigate } from "../../utils/navigation";
import { fetchWithTimeout, isTimeoutError, timeoutMessage } from "../../utils/fetchWithTimeout";

// admin-IA leaf: /admin/test-alert — 비상 신호 시뮬레이션.
// ManagementPage의 "비상 신호 시뮬레이션" 섹션(단일 트리거 팬아웃 발송)을 자기완결로 이관.
// 불변식: ① 행위 보존 ② API 무변경(바디 없는 POST /api/test-alert, admin 인증 헤더)
// ③ 게이트 상속(접근 경계는 라우팅 계약이 소유).
// notify-test(단건 채널 발송)와 별개 — 이 페이지는 등록 연락처·전 채널 팬아웃 발송이다.

function getAuthHeaders(): HeadersInit {
  const token = localStorage.getItem("token");
  return token
    ? { Authorization: `Bearer ${token}`, "Content-Type": "application/json" }
    : { "Content-Type": "application/json" };
}

export default function TestAlertPage() {
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [success, setSuccess] = useState<string | null>(null);

  const handleTestAlert = async () => {
    setError(null);
    setSuccess(null);
    setLoading(true);
    try {
      const res = await fetchWithTimeout("/api/test-alert", {
        method: "POST",
        headers: getAuthHeaders(),
      });
      if (!res.ok) {
        const data = await res.json().catch(() => ({}));
        throw new Error((data as { error?: string }).error || `HTTP ${res.status}`);
      }
      setSuccess("테스트 비상 신호가 발송되었습니다. 경보 이력에서 확인하세요.");
      setTimeout(() => setSuccess(null), 5000);
    } catch (err) {
      setError(
        isTimeoutError(err)
          ? timeoutMessage()
          : `테스트 발송 실패: ${err instanceof Error ? err.message : "알 수 없는 오류"}`
      );
    } finally {
      setLoading(false);
    }
  };

  return (
    <div className="admin-page" data-testid="admin-page" data-slug="test-alert">
      <button
        type="button"
        className="admin-back"
        data-testid="admin-back"
        onClick={() => navigate("/admin")}
      >
        ← 관리
      </button>
      <h1 className="admin-page-title">비상 신호 시뮬레이션</h1>

      <div className="mgmt-test-alert-section">
        <p className="mgmt-test-alert-desc">
          테스트 비상 신호를 발송하여 전체 알림 체인(MQTT → hw-gateway → notifier →
          KakaoTalk/SMS/이메일)을 검증합니다. 등록된 모든 연락처로 전 채널 팬아웃 발송되며,
          모든 메시지에 [테스트] 접두사가 포함됩니다.
        </p>
        {error && <p className="mgmt-form-error">{error}</p>}
        {success && <p className="mgmt-form-success">{success}</p>}
        <div className="mgmt-form-actions">
          <button
            type="button"
            className="mgmt-btn mgmt-btn-warning"
            data-testid="test-alert-trigger"
            onClick={handleTestAlert}
            disabled={loading}
          >
            {loading ? "발송 중..." : "비상 신호 시뮬레이션"}
          </button>
        </div>
      </div>
    </div>
  );
}
