import { useState } from "react";
import CCTVPage from "./pages/CCTVPage";
import IncidentsPage from "./pages/IncidentsPage";
import ManagementPage from "./pages/ManagementPage";
import SettingsPage from "./pages/SettingsPage";
import CrisisAlertBanner from "./components/CrisisAlertBanner";
import "./App.css";

type Tab = "cctv" | "incidents" | "management" | "settings";

const tabs: { key: Tab; label: string; icon: string }[] = [
  { key: "cctv", label: "CCTV", icon: "📹" },
  { key: "incidents", label: "사고이력", icon: "📋" },
  { key: "management", label: "관리", icon: "⚙️" },
  { key: "settings", label: "설정", icon: "👤" },
];

function App() {
  const [activeTab, setActiveTab] = useState<Tab>("cctv");

  const renderPage = () => {
    switch (activeTab) {
      case "cctv":
        return <CCTVPage />;
      case "incidents":
        return <IncidentsPage />;
      case "management":
        return <ManagementPage />;
      case "settings":
        return <SettingsPage />;
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
