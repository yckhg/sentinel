import { navigate } from "../../utils/navigation";

// Stub subpage (admin-IA master). Content-identity + back markers per
// verify/admin-ia/MOUNT-CONTRACT.md; the page-devices leaf fills in the body
// while preserving data-testid="admin-page", data-slug, and the admin-back
// affordance.
export default function DevicesPage() {
  return (
    <div className="admin-page" data-testid="admin-page" data-slug="devices">
      <button
        type="button"
        className="admin-back"
        data-testid="admin-back"
        onClick={() => navigate("/admin")}
      >
        ← 관리
      </button>
      <h1 className="admin-page-title">장비(센서) 관리</h1>
      <p className="admin-page-placeholder">구현 예정</p>
    </div>
  );
}
