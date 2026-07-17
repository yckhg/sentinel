import { navigate } from "../../utils/navigation";

// -----------------------------------------------------------------------------
// 관리 허브 (admin-hub.md). Entry screen of the /admin area: presents every
// management feature grouped by nature and links each to its dedicated subpage.
// The hub edits no management data — discovery / classification / navigation
// only. Static list (does not query the backend).
//
// Grouping + item↔slug mapping is copied verbatim from the admin-hub spec output
// table (assertions C/D). DOM markers follow verify/admin-ia/MOUNT-CONTRACT.md:
//   root        data-testid="admin-hub"
//   each group  data-testid="admin-hub-group" data-group="<label>"
//   each item   data-testid="admin-hub-link"  data-slug="<slug>"
// Activating an item navigates to /admin/<slug> (drilldown; routing owned by the
// seam contract).
// -----------------------------------------------------------------------------

interface HubItem {
  slug: string;
  label: string;
}
interface HubGroup {
  label: string;
  items: HubItem[];
}

const GROUPS: HubGroup[] = [
  {
    label: "장치",
    items: [
      { slug: "devices", label: "장비(센서) 관리" },
      { slug: "cameras", label: "카메라 관리" },
      { slug: "health", label: "장비 상태·예외" },
    ],
  },
  {
    label: "알림·연락",
    items: [
      { slug: "contacts", label: "비상연락망" },
      { slug: "test-alert", label: "비상 신호 시뮬레이션" },
      { slug: "notify-test", label: "알림 채널 테스트 발송" },
    ],
  },
  {
    label: "시스템",
    items: [
      { slug: "system", label: "시스템 설정(현장 정보+시스템 설정)" },
      { slug: "users", label: "사용자(계정+초대)" },
    ],
  },
  {
    label: "저장·CCTV",
    items: [
      { slug: "storage", label: "저장소 관리" },
      { slug: "cctv-links", label: "임시 CCTV 링크" },
    ],
  },
];

export default function AdminHubPage() {
  return (
    <div className="admin-hub" data-testid="admin-hub">
      <header className="admin-hub-header">
        <h1 className="admin-hub-title">관리</h1>
      </header>
      {GROUPS.map((group) => (
        <section
          key={group.label}
          className="admin-hub-group"
          data-testid="admin-hub-group"
          data-group={group.label}
          aria-label={group.label}
        >
          <h2 className="admin-hub-group-title">{group.label}</h2>
          <ul className="admin-hub-list">
            {group.items.map((item) => (
              <li key={item.slug} className="admin-hub-item">
                {/* Rendered as an anchor (role=link), not a button: the visible
                    labels are spec-mandated and some contain a tab name as a
                    substring (e.g. "시스템 설정…" ⊃ "설정"), which would collide
                    with the 4-tab getByRole("button", {name}) lookups. A link
                    role keeps the item outside those button queries. */}
                <a
                  className="admin-hub-link"
                  data-testid="admin-hub-link"
                  data-slug={item.slug}
                  href={`/admin/${item.slug}`}
                  onClick={(e) => {
                    e.preventDefault();
                    navigate(`/admin/${item.slug}`);
                  }}
                >
                  {item.label}
                </a>
              </li>
            ))}
          </ul>
        </section>
      ))}
    </div>
  );
}
