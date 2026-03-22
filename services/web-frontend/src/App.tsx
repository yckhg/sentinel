import { useState } from "react";
import CCTVPage from "./pages/CCTVPage";
import IncidentsPage from "./pages/IncidentsPage";
import ManagementPage from "./pages/ManagementPage";
import SettingsPage from "./pages/SettingsPage";
import ViewerPage from "./pages/ViewerPage";
import LoginPage from "./pages/LoginPage";
import CrisisAlertBanner from "./components/CrisisAlertBanner";
import { isTokenExpired } from "./utils/isTokenExpired";
import "./App.css";

type Tab = "cctv" | "incidents" | "management" | "settings";

const tabs: { key: Tab; label: string; icon: string }[] = [
  { key: "cctv", label: "CCTV", icon: "📹" },
  { key: "incidents", label: "사고이력", icon: "📋" },
  { key: "management", label: "관리", icon: "⚙️" },
  { key: "settings", label: "설정", icon: "👤" },
];

function getViewerToken(): string | null {
  const match = window.location.pathname.match(/^\/view\/(.+)$/);
  return match?.[1] ?? null;
}

function App() {
  const [activeTab, setActiveTab] = useState<Tab>("cctv");
  const [token, setToken] = useState<string | null>(() => {
    const stored = localStorage.getItem("token");
    if (stored && isTokenExpired(stored)) {
      localStorage.removeItem("token");
      return null;
    }
    return stored;
  });
  const viewerToken = getViewerToken();

  if (viewerToken) {
    return (
      <div className="app">
        <main className="content viewer-content">
          <ViewerPage token={viewerToken} />
        </main>
      </div>
    );
  }

  if (!token) {
    return (
      <LoginPage
        onLoginSuccess={(t) => {
          localStorage.setItem("token", t);
          setToken(t);
        }}
      />
    );
  }

  const handleLogout = () => {
    localStorage.removeItem("token");
    setToken(null);
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
      <CrisisAlertBanner />
      <main className="content">{renderPage()}</main>
      <nav className="tab-bar">
        {tabs.map((tab) => (
          <button
            key={tab.key}
            className={`tab-item${activeTab === tab.key ? " active" : ""}`}
            onClick={() => setActiveTab(tab.key)}
          >
            <span className="tab-icon">{tab.icon}</span>
            <span className="tab-label">{tab.label}</span>
          </button>
        ))}
      </nav>
    </div>
  );
}

export default App;
