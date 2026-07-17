import { navigate } from "../../utils/navigation";

// Stub subpage (admin-IA master). Filled in by the page-cctv-links leaf.
export default function CctvLinksPage() {
  return (
    <div className="admin-page" data-testid="admin-page" data-slug="cctv-links">
      <button
        type="button"
        className="admin-back"
        data-testid="admin-back"
        onClick={() => navigate("/admin")}
      >
        ← 관리
      </button>
      <h1 className="admin-page-title">임시 CCTV 링크</h1>
      <p className="admin-page-placeholder">구현 예정</p>
    </div>
  );
}
