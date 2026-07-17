import { navigate } from "../../utils/navigation";

// Stub subpage (admin-IA master). Filled in by the page-users leaf.
export default function UsersPage() {
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
      <p className="admin-page-placeholder">구현 예정</p>
    </div>
  );
}
