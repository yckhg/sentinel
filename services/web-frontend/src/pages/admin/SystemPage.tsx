import { navigate } from "../../utils/navigation";

// Stub subpage (admin-IA master). Filled in by the page-system leaf.
export default function SystemPage() {
  return (
    <div className="admin-page" data-testid="admin-page" data-slug="system">
      <button
        type="button"
        className="admin-back"
        data-testid="admin-back"
        onClick={() => navigate("/admin")}
      >
        ← 관리
      </button>
      <h1 className="admin-page-title">시스템 설정(현장 정보+시스템 설정)</h1>
      <p className="admin-page-placeholder">구현 예정</p>
    </div>
  );
}
