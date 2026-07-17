import { navigate } from "../../utils/navigation";
import DevicesSection from "../../components/DevicesSection";

// page-devices leaf (admin-IA). Mounts the canonical 장비(센서) 관리 UI
// (components/DevicesSection — behavior contract owner) at its canonical path
// /admin/devices, while preserving the mount markers required by
// verify/admin-ia/MOUNT-CONTRACT.md: root data-testid="admin-page" +
// data-slug="devices" and the data-testid="admin-back" affordance. Behavior,
// API surface, and error/status states are inherited from DevicesSection
// unchanged (3대 불변식: 행위 보존 · API 무변경 · admin 게이트 결과 상속).
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
      <DevicesSection />
    </div>
  );
}
