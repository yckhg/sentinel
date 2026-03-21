import { useState } from "react";

interface RestartDialogProps {
  cameraName: string;
  siteId: string;
  deviceId: string;
  onClose: () => void;
}

export default function RestartDialog({
  cameraName,
  siteId,
  deviceId,
  onClose,
}: RestartDialogProps) {
  const [step, setStep] = useState<1 | 2>(1);
  const [reason, setReason] = useState("");
  const [sending, setSending] = useState(false);
  const [result, setResult] = useState<{
    success: boolean;
    message: string;
  } | null>(null);

  const handleConfirm = async () => {
    setSending(true);
    try {
      const token = localStorage.getItem("token");
      const res = await fetch("/api/equipment/restart", {
        method: "POST",
        headers: {
          "Content-Type": "application/json",
          ...(token ? { Authorization: `Bearer ${token}` } : {}),
        },
        body: JSON.stringify({ siteId, deviceId, reason: reason.trim() || undefined }),
      });
      if (!res.ok) {
        const body = await res.text();
        throw new Error(body || `HTTP ${res.status}`);
      }
      setResult({ success: true, message: "재시작 명령이 전송되었습니다." });
    } catch (err) {
      setResult({
        success: false,
        message:
          err instanceof Error ? err.message : "재시작 명령 전송에 실패했습니다.",
      });
    } finally {
      setSending(false);
    }
  };

  return (
    <div className="mgmt-modal-overlay" onClick={onClose}>
      <div className="mgmt-modal" onClick={(e) => e.stopPropagation()}>
        {result ? (
          <>
            <p
              className="mgmt-modal-text"
              style={{ color: result.success ? "#2e7d32" : "#d32f2f" }}
            >
              {result.message}
            </p>
            <div className="mgmt-form-actions" style={{ justifyContent: "center" }}>
              <button className="mgmt-btn mgmt-btn-secondary" onClick={onClose}>
                닫기
              </button>
            </div>
          </>
        ) : step === 1 ? (
          <>
            <p className="mgmt-modal-text">
              <strong>{cameraName}</strong> 장비를 재시작하시겠습니까?
            </p>
            <div className="mgmt-form-actions" style={{ justifyContent: "center" }}>
              <button className="mgmt-btn mgmt-btn-secondary" onClick={onClose}>
                취소
              </button>
              <button
                className="mgmt-btn mgmt-btn-danger"
                onClick={() => setStep(2)}
              >
                재시작
              </button>
            </div>
          </>
        ) : (
          <>
            <p className="mgmt-modal-text">
              재시작 사유를 입력하고 최종 확인해 주세요.
            </p>
            <div className="mgmt-form-field">
              <label>사유 (선택)</label>
              <input
                type="text"
                value={reason}
                onChange={(e) => setReason(e.target.value)}
                placeholder="재시작 사유 입력"
                autoFocus
              />
            </div>
            <div className="mgmt-form-actions" style={{ justifyContent: "center" }}>
              <button
                className="mgmt-btn mgmt-btn-secondary"
                onClick={() => setStep(1)}
              >
                이전
              </button>
              <button
                className="mgmt-btn mgmt-btn-danger"
                onClick={handleConfirm}
                disabled={sending}
              >
                {sending ? "전송 중..." : "최종 확인"}
              </button>
            </div>
          </>
        )}
      </div>
    </div>
  );
}
