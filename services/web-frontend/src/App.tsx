import { useEffect, useReducer, useRef, useState } from "react";
import { flushSync } from "react-dom";
import CCTVPage from "./pages/CCTVPage";
import IncidentsPage from "./pages/IncidentsPage";
import SettingsPage from "./pages/SettingsPage";
import ViewerPage from "./pages/ViewerPage";
import LoginPage from "./pages/LoginPage";
import AdminHubPage from "./pages/admin/AdminHubPage";
import DevicesPage from "./pages/admin/DevicesPage";
import CamerasPage from "./pages/admin/CamerasPage";
import HealthPage from "./pages/admin/HealthPage";
import ContactsPage from "./pages/admin/ContactsPage";
import TestAlertPage from "./pages/admin/TestAlertPage";
import NotifyTestPage from "./pages/admin/NotifyTestPage";
import SystemPage from "./pages/admin/SystemPage";
import UsersPage from "./pages/admin/UsersPage";
import StoragePage from "./pages/admin/StoragePage";
import CctvLinksPage from "./pages/admin/CctvLinksPage";
import CrisisAlertBanner from "./components/CrisisAlertBanner";
import ErrorBoundary from "./components/ErrorBoundary";
import { isTokenExpired, isAdmin } from "./utils/jwt";
import { navigate } from "./utils/navigation";
import "./App.css";

type Tab = "cctv" | "incidents" | "management" | "settings";

// URL is the single source of truth for the active tab (spec §핵심로직 라우팅).
// canonical path strings — exactly these four.
const PATH_TO_TAB: Record<string, Tab> = {
  "/cctv": "cctv",
  "/incidents": "incidents",
  "/admin": "management",
  "/settings": "settings",
};
const TAB_TO_PATH: Record<Tab, string> = {
  cctv: "/cctv",
  incidents: "/incidents",
  management: "/admin",
  settings: "/settings",
};
const DEFAULT_PATH = "/cctv";

// --- Admin subtree (admin-routing-navigation-contract) ---
// The slug allowlist is exactly these 10 (SSOT for the /admin/<slug> resolver,
// gate, returnTo guard, and 404 fallback). Owned by the master; leaves never
// edit this table.
const ADMIN_SLUGS = [
  "devices",
  "cameras",
  "health",
  "contacts",
  "test-alert",
  "notify-test",
  "system",
  "users",
  "storage",
  "cctv-links",
] as const;
type AdminSlug = (typeof ADMIN_SLUGS)[number];

// slug ↔ subpage component (master-owned route table). Each is a props-less
// default export mounted 1:1 on its canonical /admin/<slug> path.
const ADMIN_PAGES: Record<AdminSlug, () => JSX.Element> = {
  devices: DevicesPage,
  cameras: CamerasPage,
  health: HealthPage,
  contacts: ContactsPage,
  "test-alert": TestAlertPage,
  "notify-test": NotifyTestPage,
  system: SystemPage,
  users: UsersPage,
  storage: StoragePage,
  "cctv-links": CctvLinksPage,
};

// Every /admin/<slug> deep path (known slugs) is protected & role-gated too.
const ADMIN_SLUG_PATHS = ADMIN_SLUGS.map((s) => `/admin/${s}`);
// Protected = the 4 canonical tab paths (incl. /admin) + every known admin slug
// path. Auth-gated (unauth → login) and returnTo-allowlisted.
const PROTECTED_PATHS = [...Object.keys(PATH_TO_TAB), ...ADMIN_SLUG_PATHS];

const tabs: { key: Tab; label: string; icon: string }[] = [
  { key: "cctv", label: "CCTV", icon: "📹" },
  { key: "incidents", label: "경보이력", icon: "📋" },
  { key: "management", label: "관리", icon: "⚙️" },
  { key: "settings", label: "설정", icon: "👤" },
];

function getViewerToken(pathname: string): string | null {
  const match = pathname.match(/^\/view\/(.+)$/);
  return match?.[1] ?? null;
}

// Parse an /admin/<slug> path. Returns the raw slug segment (may be an unknown
// slug — the caller checks the allowlist) or null when not an admin subpath.
function getAdminSlug(pathname: string): string | null {
  const match = pathname.match(/^\/admin\/([^/]+)$/);
  return match?.[1] ?? null;
}

// Open-redirect guard: only in-app canonical protected paths are valid returnTo.
// The allowlist now includes /admin + every /admin/<slug>; external / arbitrary
// URLs are still rejected (fall back to DEFAULT_PATH).
function sanitizeReturnTo(raw: string | null): string | null {
  if (!raw) return null;
  return PROTECTED_PATHS.includes(raw) ? raw : null;
}

function App() {
  // Bump on every navigation / popstate; window.location is read live so it
  // is always in sync (pathname + search).
  const [, forceRoute] = useReducer((x) => x + 1, 0);
  const [token, setToken] = useState<string | null>(() => {
    const stored = localStorage.getItem("token");
    if (stored && isTokenExpired(stored)) {
      localStorage.removeItem("token");
      return null;
    }
    return stored;
  });
  const deepLinkSeeded = useRef(false);

  useEffect(() => {
    const onPop = () => forceRoute();
    window.addEventListener("popstate", onPop);
    return () => window.removeEventListener("popstate", onPop);
  }, []);

  const pathname = window.location.pathname;
  const isAuthed = !!token;

  // --- Admin route resolution (independent of the 4-tab enum) ---
  const isAdminHub = pathname === "/admin";
  const rawAdminSlug = getAdminSlug(pathname);
  const adminSlug: AdminSlug | null =
    rawAdminSlug && (ADMIN_SLUGS as readonly string[]).includes(rawAdminSlug)
      ? (rawAdminSlug as AdminSlug)
      : null;
  // In-allowlist admin content routes (hub + known subpages). An /admin/<unknown>
  // is deliberately excluded so it falls through to the 404 fallback.
  const isAdminArea = isAdminHub || adminSlug !== null;

  // --- Redirects handled as side effects (history.replace, no loop) ---
  const isProtected = PROTECTED_PATHS.includes(pathname);
  const needsAuthRedirect = isProtected && !isAuthed;
  // Route-level role==admin gate: authed non-admin on any admin route is bounced
  // to DEFAULT_PATH without ever rendering management content.
  const needsAdminRedirect = isAdminArea && isAuthed && !isAdmin(token);
  const isRootRedirect = pathname === "/";

  useEffect(() => {
    if (needsAuthRedirect) {
      navigate(`/login?returnTo=${encodeURIComponent(pathname)}`, { replace: true });
    } else if (needsAdminRedirect) {
      navigate(DEFAULT_PATH, { replace: true });
    } else if (isRootRedirect) {
      navigate(isAuthed ? DEFAULT_PATH : "/login", { replace: true });
    }
  }, [needsAuthRedirect, needsAdminRedirect, isRootRedirect, isAuthed, pathname]);

  // Deep-link history seeding: when the app is loaded directly onto an
  // /admin/<slug> (no in-app navigation brought us here — history.state carries
  // no __appNav marker), seed /admin as the prior history entry so browser Back
  // lands on the hub (seam-C). Pre-push (not replace): rewrite the current entry
  // to /admin, then push the slug back on top. Runs once, only for an admin who
  // will actually render the subpage.
  useEffect(() => {
    if (
      adminSlug !== null &&
      isAuthed &&
      isAdmin(token) &&
      !deepLinkSeeded.current &&
      !(window.history.state && window.history.state.__appNav)
    ) {
      deepLinkSeeded.current = true;
      window.history.replaceState({ __appNav: true }, "", "/admin");
      window.history.pushState({ __appNav: true }, "", pathname);
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const handleLoginSuccess = (t: string, mode: "login" | "register") => {
    localStorage.setItem("token", t);
    // Commit the authed state BEFORE navigating. navigate() mutates
    // window.location (the live-read routing source) and synchronously
    // dispatches popstate; without flushSync the ensuing render would observe
    // the new protected path while `token` state still lags (isAuthed=false),
    // firing the auth-redirect effect and bouncing back to /login. flushSync
    // guarantees the target path is only ever rendered with the authed token.
    flushSync(() => setToken(t));
    if (mode === "register") {
      navigate(DEFAULT_PATH, { replace: true });
      return;
    }
    // login → returnTo (replace so Back does not land on /login or a redirect stub)
    const params = new URLSearchParams(window.location.search);
    const target = sanitizeReturnTo(params.get("returnTo")) ?? DEFAULT_PATH;
    navigate(target, { replace: true });
  };

  // --- Route resolution ---

  // Viewer temp link — token IS the auth, works regardless of app JWT.
  const viewerToken = getViewerToken(pathname);
  if (viewerToken) {
    return (
      <div className="app">
        <main className="content viewer-content">
          <ViewerPage token={viewerToken} />
        </main>
      </div>
    );
  }

  // Registration (invite acceptance) — keep working even when logged in.
  if (pathname === "/register") {
    return <LoginPage onLoginSuccess={(t) => handleLoginSuccess(t, "register")} />;
  }

  // Login route.
  if (pathname === "/login") {
    return <LoginPage onLoginSuccess={(t) => handleLoginSuccess(t, "login")} />;
  }

  // A redirect effect is in flight — render nothing (no content leak):
  //  - root / protected-without-auth  → auth redirect
  //  - authed non-admin on admin route → gate redirect to DEFAULT_PATH
  if (isRootRedirect || needsAuthRedirect || needsAdminRedirect) {
    return null;
  }

  // Canonical protected paths (authed). Includes the 4 tabs + the admin subtree
  // (hub + known subpages); the admin gate above already vetted role==admin.
  if (isProtected && isAuthed) {
    // Admin subpages are not in the 4-tab enum; keep the 관리 tab active for the
    // whole admin subtree.
    const activeTab: Tab = PATH_TO_TAB[pathname] ?? "management";

    const handleLogout = () => {
      localStorage.removeItem("token");
      setToken(null);
      navigate("/login", { replace: true });
    };

    const renderPage = () => {
      if (isAdminHub) return <AdminHubPage />;
      if (adminSlug !== null) {
        const Page = ADMIN_PAGES[adminSlug];
        return <Page />;
      }
      switch (activeTab) {
        case "cctv":
          return <CCTVPage />;
        case "incidents":
          return <IncidentsPage />;
        case "settings":
          return <SettingsPage onLogout={handleLogout} />;
        case "management":
          // /admin is handled by isAdminHub above; unreachable fallback.
          return <AdminHubPage />;
      }
    };

    return (
      <div className="app">
        {/* The crisis banner is safety-critical — isolate it so a page render
            error can't take it (or the nav) down with it (#99). */}
        <ErrorBoundary label="banner">
          <CrisisAlertBanner />
        </ErrorBoundary>
        <main className="content">
          {/* key={pathname} remounts the boundary on any route change, clearing a
              previous page's error automatically. */}
          <ErrorBoundary key={pathname} label={`page:${activeTab}`}>
            {renderPage()}
          </ErrorBoundary>
        </main>
        <nav className="tab-bar" aria-label="주요 메뉴">
          {tabs.map((tab) => (
            <button
              key={tab.key}
              className={`tab-item${activeTab === tab.key ? " active" : ""}`}
              aria-current={activeTab === tab.key ? "page" : undefined}
              onClick={() => navigate(TAB_TO_PATH[tab.key])}
            >
              <span className="tab-icon" aria-hidden="true">{tab.icon}</span>
              <span className="tab-label">{tab.label}</span>
            </button>
          ))}
        </nav>
      </div>
    );
  }

  // Unknown path → 404 (never absorbed into the main app / CCTV). Includes
  // /admin/<unknown> (slug ∉ allowlist): not-found, tab bar NOT rendered (seam-F).
  return (
    <div className="app">
      <main className="content not-found" data-view="not-found">
        <div className="not-found-box">
          <h1 className="not-found-code">404</h1>
          <p className="not-found-text">요청하신 페이지를 찾을 수 없습니다.</p>
          <button
            className="mgmt-btn mgmt-btn-primary"
            onClick={() => navigate(isAuthed ? DEFAULT_PATH : "/login", { replace: true })}
          >
            홈으로 이동
          </button>
        </div>
      </main>
    </div>
  );
}

export default App;
