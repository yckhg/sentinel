import { navigate } from "../../utils/navigation";
import HealthPanel from "../../components/HealthPanel";

// Admin-IA leaf: /admin/health — 장비 상태·예외 (read-only current-state summary).
// Content-identity + back markers per verify/admin-ia/MOUNT-CONTRACT.md; the body
// is the relocated system-status panel, owned by HealthPanel (spec admin-page-health).
export default function HealthPage() {
  return (
    <div className="admin-page" data-testid="admin-page" data-slug="health">
      <button
        type="button"
        className="admin-back"
        data-testid="admin-back"
        onClick={() => navigate("/admin")}
      >
        ← 관리
      </button>
      <h1 className="admin-page-title">장비 상태·예외</h1>
      <HealthPanel />
    </div>
  );
}
