import { useEffect, useState } from "react";
import HLSPlayer from "../components/HLSPlayer";

interface Camera {
  id: number;
  name: string;
  location: string;
  zone: string;
  hlsUrl: string;
  status: "connected" | "disconnected";
}

export default function CCTVPage() {
  const [cameras, setCameras] = useState<Camera[]>([]);
  const [expandedId, setExpandedId] = useState<number | null>(null);
  const [loading, setLoading] = useState(true);
  const [fetchError, setFetchError] = useState<string | null>(null);

  useEffect(() => {
    let cancelled = false;

    const fetchCameras = async () => {
      try {
        const token = localStorage.getItem("token");
        const res = await fetch("/api/cameras", {
          headers: token ? { Authorization: `Bearer ${token}` } : {},
        });
        if (!res.ok) throw new Error(`HTTP ${res.status}`);
        const data: Camera[] = await res.json();
        if (!cancelled) {
          setCameras(data);
          setFetchError(null);
        }
      } catch (err) {
        if (!cancelled) {
          setFetchError(
            err instanceof Error ? err.message : "카메라 목록을 불러올 수 없습니다"
          );
        }
      } finally {
        if (!cancelled) setLoading(false);
      }
    };

    fetchCameras();
    const interval = setInterval(fetchCameras, 30000);

    return () => {
      cancelled = true;
      clearInterval(interval);
    };
  }, []);

  const handleToggleExpand = (id: number) => {
    setExpandedId((prev) => (prev === id ? null : id));
  };

  if (loading) {
    return (
      <div className="page">
        <h2>CCTV</h2>
        <p className="cctv-loading">카메라 목록 로딩 중...</p>
      </div>
    );
  }

  if (fetchError) {
    return (
      <div className="page">
        <h2>CCTV</h2>
        <p className="cctv-error">{fetchError}</p>
      </div>
    );
  }

  if (cameras.length === 0) {
    return (
      <div className="page">
        <h2>CCTV</h2>
        <p className="cctv-empty">등록된 카메라가 없습니다</p>
      </div>
    );
  }

  return (
    <div className="page">
      <h2>CCTV</h2>
      <div className={`camera-grid${expandedId !== null ? " has-expanded" : ""}`}>
        {cameras.map((cam) => (
          <HLSPlayer
            key={cam.id}
            url={cam.hlsUrl}
            cameraName={cam.name}
            zone={cam.zone}
            status={cam.status}
            expanded={expandedId === cam.id}
            onToggleExpand={() => handleToggleExpand(cam.id)}
          />
        ))}
      </div>
    </div>
  );
}
