import { navigate } from "../../utils/navigation";

// Stub subpage (admin-IA master). Filled in by the page-cameras leaf.
export default function CamerasPage() {
  return (
    <div className="admin-page" data-testid="admin-page" data-slug="cameras">
      <button
        type="button"
        className="admin-back"
        data-testid="admin-back"
        onClick={() => navigate("/admin")}
      >
        ← 관리
      </button>
      <h1 className="admin-page-title">카메라 관리</h1>
      <p className="admin-page-placeholder">구현 예정</p>
    </div>
  );
}
