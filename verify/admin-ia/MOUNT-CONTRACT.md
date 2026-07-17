# Admin-IA DOM mount contract (shared test-ID surface)

This file fixes the **DOM markers** that the master (hub + routing), every leaf
subpage, and the Playwright gate specs all agree on. It is a thin naming contract
so the seam A~I / hub A~G gates and per-leaf identity gates can locate elements
without guessing. Behavior/logic contracts live in `docs/spec/`; this is only the
observable marker surface.

Slug set (exactly these 10, from the seam contract):
`devices`, `cameras`, `health`, `contacts`, `test-alert`, `notify-test`,
`system`, `users`, `storage`, `cctv-links`.

## Hub (`/admin`) — owned by master (`pages/admin/AdminHubPage.tsx`)

- Hub root element: `data-testid="admin-hub"`.
- Exactly 4 group-label elements, each `data-testid="admin-hub-group"` with
  `data-group="<label>"` where `<label>` ∈ { `장치`, `알림·연락`, `시스템`, `저장·CCTV` }.
- Exactly 10 item affordances, each `data-testid="admin-hub-link"` with
  `data-slug="<slug>"`. Activating one navigates to `/admin/<slug>`.
- Each item sits under its correct group per the hub spec output table (assertion C).

## Subpage (`/admin/<slug>`) — root marker owned by master stub, preserved by leaf

- Each subpage's outermost element carries `data-testid="admin-page"` and
  `data-slug="<slug>"`. This is the **content-identity anchor**: seam B/hub E judge
  "a subpage (not the hub, not blank, not a crash) rendered for this slug" by the
  presence of `admin-page` with the matching `data-slug`, and URL == `/admin/<slug>`.
- Each subpage has an in-page back affordance: `data-testid="admin-back"`. Clicking
  it navigates to `/admin` (seam C normative mechanism). The master stub wires this;
  leaves MUST keep it.
- The master ships each subpage as a **stub** (default export, no props) carrying the
  two markers above + a heading naming the feature. Each leaf unit REPLACES the body
  of its own `pages/admin/<Slug>Page.tsx` with the real relocated section, keeping
  `data-testid="admin-page"`, `data-slug`, and the `admin-back` affordance intact.
- Leaves add their own feature-specific markers for behavior gates as needed; those
  are owned by each leaf spec, not here.

## Not-found fallback (unchanged, owned by App.tsx)

- `/admin/<unknown>` (slug ∉ allowlist) renders the existing 404:
  `data-view="not-found"`, and the 4-tab bar is NOT rendered (seam F).

## Component file map (ownership)

| slug | component file | owning unit |
|---|---|---|
| (hub) | `pages/admin/AdminHubPage.tsx` | master |
| devices | `pages/admin/DevicesPage.tsx` | page-devices |
| cameras | `pages/admin/CamerasPage.tsx` | page-cameras |
| health | `pages/admin/HealthPage.tsx` | page-health |
| contacts | `pages/admin/ContactsPage.tsx` | page-contacts |
| test-alert | `pages/admin/TestAlertPage.tsx` | page-test-alert |
| notify-test | `pages/admin/NotifyTestPage.tsx` | page-notify-test |
| system | `pages/admin/SystemPage.tsx` | page-system |
| users | `pages/admin/UsersPage.tsx` | page-users |
| storage | `pages/admin/StoragePage.tsx` | page-storage |
| cctv-links | `pages/admin/CctvLinksPage.tsx` | page-cctv-links |

App.tsx route table + slug↔component registration is **master-owned**; leaves never
edit App.tsx. Master imports all 10 component files (so it compiles it ships stubs;
leaves fill them in). `utils/phone.ts` (PHONE_REGEX + live formatter) is master-owned;
contacts/system import it.
