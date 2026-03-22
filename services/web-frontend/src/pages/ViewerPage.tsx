import { useEffect, useState } from "react";
import HLSPlayer from "../components/HLSPlayer";
import { fetchWithTimeout, isTimeoutError, timeoutMessage } from "../utils/fetchWithTimeout";

interface Camera {
  id: number;
  name: string;
  location: string;
  zone: string;
  hlsUrl: string;
  status: "connected" | "disconnected";
}

type ViewerState = "loading" | "valid" | "invalid";

export default function ViewerPage({ token }: { token: string }) {
  const [state, setState] = useState<ViewerState>("loading");
  const [errorMsg, setErrorMsg] = useState("");
  const [cameras, setCameras] = useState<Camera[]>([]);
  const [expandedId, setExpandedId] = useState<number | null>(null);
  const [camerasLoading, setCamerasLoading] = useState(true);
  const [cameraError, setCameraError] = useState<string | null>(null);

  useEffect(() => {
    const verify = async () => {
      try {
        const res = await fetchWithTimeout(`/api/links/verify/${token}`);
        if (!res.ok) {
          setState("invalid");
          setErrorMsg(
            res.status === 401
              ? "링크가 만료되었거나 유효하지 않습니다"
              : "링크 확인에 실패했습니다"
          );
          return;
        }
        setState("valid");
      } catch (err) {
        setState("invalid");
        setErrorMsg(isTimeoutError(err) ? timeoutMessage() : "서버에 연결할 수 없습니다");
      }
    };
    verify();
  }, [token]);

  useEffect(() => {
    if (state !== "valid") return;

    const controller = new AbortController();

    const fetchCameras = async () => {
      try {
        const res = await fetchWithTimeout("/api/cameras", {
          headers: { Authorization: `Bearer ${token}` },
          signal: controller.signal,
        });
        if (!res.ok) throw new Error(`HTTP ${res.status}`);
        const data: Camera[] = await res.json();
        setCameras(data);
        setCameraError(null);
      } catch (err) {
        if (controller.signal.aborted) return;
        setCameraError(
          isTimeoutError(err)
            ? timeoutMessage()
            : err instanceof Error ? err.message : "카메라 목록을 불러올 수 없습니다"
        );
      } finally {
        if (!controller.signal.aborted) setCamerasLoading(false);
      }
    };

    fetchCameras();
    const interval = setInterval(fetchCameras, 30000);

    return () => {
      controller.abort();
      clearInterval(interval);
    };
  }, [state, token]);

  if (state === "loading") {
    return (
      <div className="viewer-page">
        <p className="viewer-status">링크 확인 중...</p>
      </div>
    );
  }

  if (state === "invalid") {
    return (
      <div className="viewer-page">
        <div className="viewer-error">
          <span className="viewer-error-icon">⚠</span>
          <h2>접근 불가</h2>
          <p>{errorMsg}</p>
        </div>
      </div>
    );
  }

  if (camerasLoading) {
    return (
      <div className="viewer-page">
        <div className="viewer-header">
          <h2>CCTV 실시간 모니터링</h2>
        </div>
        <p className="viewer-status">카메라 목록 로딩 중...</p>
      </div>
    );
  }

  if (cameraError && cameras.length === 0) {
    return (
      <div className="viewer-page">
        <div className="viewer-header">
          <h2>CCTV 실시간 모니터링</h2>
        </div>
        <p className="cctv-error">{cameraError}</p>
      </div>
    );
  }

  if (cameras.length === 0) {
    return (
      <div className="viewer-page">
        <div className="viewer-header">
          <h2>CCTV 실시간 모니터링</h2>
        </div>
        <p className="viewer-status">등록된 카메라가 없습니다</p>
      </div>
    );
  }

  return (
    <div className="viewer-page">
      <div className="viewer-header">
        <h2>CCTV 실시간 모니터링</h2>
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
            onToggleExpand={() =>
              setExpandedId((prev) => (prev === cam.id ? null : cam.id))
            }
          />
        ))}
      </div>
    </div>
  );
}
