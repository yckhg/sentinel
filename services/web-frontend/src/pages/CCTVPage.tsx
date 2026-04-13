import { useEffect, useState } from "react";
import HLSPlayer from "../components/HLSPlayer";
import RecordingTimeline from "../components/RecordingTimeline";
import RestartDialog from "../components/RestartDialog";
import EmergencyCallButton from "../components/EmergencyCallButton";
import { fetchWithTimeout, isTimeoutError, timeoutMessage } from "../utils/fetchWithTimeout";

interface Camera {
  id: number;
  name: string;
  location: string;
  zone: string;
  hlsUrl: string;
  streamKey: string;
  status: "connected" | "disconnected";
  siteId?: string;
  deviceId?: string;
}

export default function CCTVPage() {
  const [cameras, setCameras] = useState<Camera[]>([]);
  const [expandedId, setExpandedId] = useState<number | null>(null);
  const [loading, setLoading] = useState(true);
  const [fetchError, setFetchError] = useState<string | null>(null);
  const [restartCamera, setRestartCamera] = useState<Camera | null>(null);
  const [playbackUrl, setPlaybackUrl] = useState<string | null>(null);

  useEffect(() => {
    const controller = new AbortController();

    const fetchCameras = async () => {
      try {
        const token = localStorage.getItem("token");
        const res = await fetchWithTimeout("/api/cameras", {
          headers: token ? { Authorization: `Bearer ${token}` } : {},
          signal: controller.signal,
        });
        if (!res.ok) throw new Error(`HTTP ${res.status}`);
        const data: Camera[] = await res.json();
        setCameras(data);
        setFetchError(null);
      } catch (err) {
        if (controller.signal.aborted) return;
        setFetchError(
          isTimeoutError(err)
            ? timeoutMessage()
            : err instanceof Error
              ? err.message
              : "카메라 목록을 불러올 수 없습니다"
        );
      } finally {
        if (!controller.signal.aborted) setLoading(false);
      }
    };

    fetchCameras();
    const interval = setInterval(fetchCameras, 30000);

    return () => {
      controller.abort();
      clearInterval(interval);
    };
  }, []);

  const handleToggleExpand = (id: number) => {
    setExpandedId((prev) => {
      if (prev === id) {
        // Collapsing — return to live
        setPlaybackUrl(null);
        return null;
      }
      // Expanding a different camera — reset playback
      setPlaybackUrl(null);
      return id;
    });
  };

  const handlePlaybackRequest = (url: string | null) => {
    setPlaybackUrl(url);
  };

  const handleRestartClick = (cam: Camera) => {
    // 정책: device 액션은 해당 device row의 site_id를 그대로 사용한다
    // (architecture-overview.md "운영 정책 A — Single-tenant" 참조).
    // siteId/deviceId가 비어있으면 backend가 매핑할 수 없으므로 사용자에게 알린다.
    if (!cam.siteId || !cam.deviceId) {
      alert(
        "이 카메라에는 device 매핑(siteId/deviceId)이 없어 재시작 명령을 보낼 수 없습니다.\n" +
          "관리 탭에서 카메라-device 매핑을 확인하세요."
      );
      return;
    }
    setRestartCamera(cam);
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
      <div className="cctv-header">
        <h2>CCTV</h2>
        <EmergencyCallButton />
      </div>
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
            onRestart={() => handleRestartClick(cam)}
            playbackUrl={expandedId === cam.id ? playbackUrl : null}
          />
        ))}
      </div>
      {expandedId !== null && (() => {
        const cam = cameras.find((c) => c.id === expandedId);
        return cam ? (
          <RecordingTimeline
            streamKey={cam.streamKey}
            onPlaybackRequest={handlePlaybackRequest}
            isPlaying={!!playbackUrl}
          />
        ) : null;
      })()}
      {restartCamera && (
        <RestartDialog
          cameraName={restartCamera.name}
          siteId={restartCamera.siteId!}
          deviceId={restartCamera.deviceId!}
          onClose={() => setRestartCamera(null)}
        />
      )}
    </div>
  );
}
