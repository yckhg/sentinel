interface SettingsPageProps {
  onLogout: () => void;
}

export default function SettingsPage({ onLogout }: SettingsPageProps) {
  return (
    <div className="page">
      <h2>설정</h2>
      <p style={{ color: "#666", marginBottom: "1rem" }}>계정 및 시스템 설정</p>
      <button className="mgmt-btn mgmt-btn-danger" onClick={onLogout}>
        로그아웃
      </button>
    </div>
  );
}
