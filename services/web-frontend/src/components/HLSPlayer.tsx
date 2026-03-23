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
  playbackUrl?: string | null;
}

export default function HLSPlayer({
  url,
  cameraName,
  zone,
  status,
  expanded,
  onToggleExpand,
  onRestart,
  playbackUrl,
}: HLSPlayerProps) {
  const videoRef = useRef<HTMLVideoElement>(null);
  const hlsRef = useRef<Hls | null>(null);
  const [error, setError] = useState(false);

  const activeUrl = playbackUrl || url;
  const isPlayback = !!playbackUrl;

  useEffect(() => {
    const video = videoRef.current;
    if (!video) return;

    // Destroy any existing HLS instance before creating a new one or on disconnect
    if (hlsRef.current) {
      hlsRef.current.destroy();
      hlsRef.current = null;
    }

    if (status === "disconnected" && !isPlayback) return;

    setError(false);

    if (Hls.isSupported()) {
      const hlsConfig = isPlayback
        ? { enableWorker: true }
        : {
            enableWorker: true,
            lowLatencyMode: true,
            liveSyncDurationCount: 2,
            liveMaxLatencyDurationCount: 4,
          };
      const hls = new Hls(hlsConfig);
      hlsRef.current = hls;
      hls.loadSource(activeUrl);
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
          hlsRef.current = null;
        }
      });

      return () => {
        hls.destroy();
        hlsRef.current = null;
      };
    } else if (video.canPlayType("application/vnd.apple.mpegurl")) {
      video.src = activeUrl;
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
  }, [activeUrl, status, isPlayback]);

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
      {isPlayback && (
        <div className="camera-playback-badge">녹화 재생</div>
      )}
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
