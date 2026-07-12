import { useEffect, useReducer, useState } from "react";
import { flushSync } from "react-dom";
import CCTVPage from "./pages/CCTVPage";
import IncidentsPage from "./pages/IncidentsPage";
import ManagementPage from "./pages/ManagementPage";
import SettingsPage from "./pages/SettingsPage";
import ViewerPage from "./pages/ViewerPage";
import LoginPage from "./pages/LoginPage";
import CrisisAlertBanner from "./components/CrisisAlertBanner";
import ErrorBoundary from "./components/ErrorBoundary";
import { isTokenExpired } from "./utils/jwt";
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
const PROTECTED_PATHS = Object.keys(PATH_TO_TAB);

const tabs: { key: Tab; label: string; icon: string }[] = [
  { key: "cctv", label: "CCTV", icon: "📹" },
  { key: "incidents", label: "사고이력", icon: "📋" },
  { key: "management", label: "관리", icon: "⚙️" },
  { key: "settings", label: "설정", icon: "👤" },
];

function getViewerToken(pathname: string): string | null {
  const match = pathname.match(/^\/view\/(.+)$/);
  return match?.[1] ?? null;
}

// Open-redirect guard: only in-app canonical protected paths are valid returnTo.
function sanitizeReturnTo(raw: string | null): string | null {
  if (!raw) return null;
  return PROTECTED_PATHS.includes(raw) ? raw : null;
}

function navigate(to: string, opts: { replace?: boolean } = {}) {
  if (opts.replace) window.history.replaceState({}, "", to);
  else window.history.pushState({}, "", to);
  window.dispatchEvent(new PopStateEvent("popstate"));
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

  useEffect(() => {
    const onPop = () => forceRoute();
    window.addEventListener("popstate", onPop);
    return () => window.removeEventListener("popstate", onPop);
  }, []);

  const pathname = window.location.pathname;
  const isAuthed = !!token;

  // --- Redirects handled as side effects (history.replace, no loop) ---
  const isProtected = PROTECTED_PATHS.includes(pathname);
  const needsAuthRedirect = isProtected && !isAuthed;
  const isRootRedirect = pathname === "/";

  useEffect(() => {
    if (needsAuthRedirect) {
      navigate(`/login?returnTo=${encodeURIComponent(pathname)}`, { replace: true });
    } else if (isRootRedirect) {
      navigate(isAuthed ? DEFAULT_PATH : "/login", { replace: true });
    }
  }, [needsAuthRedirect, isRootRedirect, isAuthed, pathname]);

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

  // Root / protected-without-auth: a redirect effect is in flight — render nothing.
  if (isRootRedirect || needsAuthRedirect) {
    return null;
  }

  // Canonical protected tab paths (authed).
  if (isProtected && isAuthed) {
    const activeTab = PATH_TO_TAB[pathname];

    const handleLogout = () => {
      localStorage.removeItem("token");
      setToken(null);
      navigate("/login", { replace: true });
    };

    const renderPage = () => {
      switch (activeTab) {
        case "cctv":
          return <CCTVPage />;
        case "incidents":
          return <IncidentsPage />;
        case "management":
          return <ManagementPage />;
        case "settings":
          return <SettingsPage onLogout={handleLogout} />;
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
          {/* key={activeTab} remounts the boundary on tab change, clearing a
              previous page's error automatically. */}
          <ErrorBoundary key={activeTab} label={`page:${activeTab}`}>
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

  // Unknown path → 404 (never absorbed into the main app / CCTV).
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
