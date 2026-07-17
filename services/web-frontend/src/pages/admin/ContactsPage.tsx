import { navigate } from "../../utils/navigation";

// Stub subpage (admin-IA master). Filled in by the page-contacts leaf.
export default function ContactsPage() {
  return (
    <div className="admin-page" data-testid="admin-page" data-slug="contacts">
      <button
        type="button"
        className="admin-back"
        data-testid="admin-back"
        onClick={() => navigate("/admin")}
      >
        ← 관리
      </button>
      <h1 className="admin-page-title">비상연락망</h1>
      <p className="admin-page-placeholder">구현 예정</p>
    </div>
  );
}
