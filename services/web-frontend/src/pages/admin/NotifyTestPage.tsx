import { navigate } from "../../utils/navigation";

// Stub subpage (admin-IA master). Filled in by the page-notify-test leaf.
export default function NotifyTestPage() {
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
      <p className="admin-page-placeholder">구현 예정</p>
    </div>
  );
}
