import { useEffect, useRef, useState } from "react";
import Hls from "hls.js";

interface HLSPlayerProps {
  url: string;
  cameraName: string;
  zone: string;
  status: "connected" | "disconnected";
  expanded: boolean;
  onToggleExpand: () => void;
  onRestart?: () => void;
}

export default function HLSPlayer({
  url,
  cameraName,
  zone,
  status,
  expanded,
  onToggleExpand,
  onRestart,
}: HLSPlayerProps) {
  const videoRef = useRef<HTMLVideoElement>(null);
  const hlsRef = useRef<Hls | null>(null);
  const [error, setError] = useState(false);

  useEffect(() => {
    const video = videoRef.current;
    if (!video || status === "disconnected") return;

    setError(false);

    if (Hls.isSupported()) {
      const hls = new Hls({
        enableWorker: true,
        lowLatencyMode: true,
        liveSyncDurationCount: 2,
        liveMaxLatencyDurationCount: 4,
      });
      hlsRef.current = hls;
      hls.loadSource(url);
      hls.attachMedia(video);
      hls.on(Hls.Events.MANIFEST_PARSED, () => {
        video.play().catch(() => {
          /* autoplay blocked */
        });
      });
      hls.on(Hls.Events.ERROR, (_event, data) => {
        if (data.fatal) {
          setError(true);
          hls.destroy();
        }
      });

      return () => {
        hls.destroy();
        hlsRef.current = null;
      };
    } else if (video.canPlayType("application/vnd.apple.mpegurl")) {
      video.src = url;
      video.addEventListener("loadedmetadata", () => {
        video.play().catch(() => {});
      });
      const onError = () => setError(true);
      video.addEventListener("error", onError);
      return () => {
        video.removeEventListener("error", onError);
        video.src = "";
      };
    }
  }, [url, status]);

  const disconnected = status === "disconnected" || error;

  return (
    <div
      className={`camera-cell${expanded ? " expanded" : ""}`}
      onClick={onToggleExpand}
    >
      <video
        ref={videoRef}
        className="camera-video"
        muted
        playsInline
        autoPlay
      />
      <div className="camera-overlay">
        <span className="camera-name">{cameraName}</span>
        <span className="camera-zone">{zone}</span>
      </div>
      {expanded && onRestart && (
        <button
          className="camera-restart-btn"
          onClick={(e) => {
            e.stopPropagation();
            onRestart();
          }}
          title="장비 재시작"
        >
          &#x21BB;
        </button>
      )}
      {disconnected && (
        <div className="camera-disconnected">
          <span className="disconnected-icon">&#x26A0;</span>
          <span>연결 끊김</span>
        </div>
      )}
    </div>
  );
}
