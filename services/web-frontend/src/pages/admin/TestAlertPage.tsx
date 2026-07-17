import { navigate } from "../../utils/navigation";

// Stub subpage (admin-IA master). Filled in by the page-test-alert leaf.
export default function TestAlertPage() {
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
      <p className="admin-page-placeholder">구현 예정</p>
    </div>
  );
}
