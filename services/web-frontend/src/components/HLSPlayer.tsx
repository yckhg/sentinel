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
  // Bumped by the manual "재시도" button to force a fresh HLS init.
  const [reloadKey, setReloadKey] = useState(0);
  // Auto-recovery attempts for the current init; reset on each (re)init.
  const retryCountRef = useRef(0);
  const MAX_AUTO_RETRIES = 3;

  const activeUrl = playbackUrl || url;
  const isPlayback = !!playbackUrl;

  const handleRetry = (e: React.MouseEvent) => {
    e.stopPropagation();
    retryCountRef.current = 0;
    setError(false);
    setReloadKey((k) => k + 1);
  };

  useEffect(() => {
    const video = videoRef.current;
    if (!video) return;

    retryCountRef.current = 0;

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
        if (!data.fatal) return;
        // Transient network/media glitches (e.g. brief connectivity loss on a
        // live stream) are recoverable — try a few times before giving up so
        // the tile doesn't stay dead until the 30s status poll re-inits (#96).
        if (retryCountRef.current < MAX_AUTO_RETRIES) {
          retryCountRef.current += 1;
          if (data.type === Hls.ErrorTypes.NETWORK_ERROR) {
            hls.startLoad();
            return;
          }
          if (data.type === Hls.ErrorTypes.MEDIA_ERROR) {
            hls.recoverMediaError();
            return;
          }
        }
        // Non-recoverable, or retries exhausted → surface with a manual retry.
        setError(true);
        hls.destroy();
        hlsRef.current = null;
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
  }, [activeUrl, status, isPlayback, reloadKey]);

  const disconnected = status === "disconnected" || error;

  return (
    <div
      className={`camera-cell${expanded ? " expanded" : ""}`}
      role="button"
      tabIndex={0}
      aria-pressed={expanded}
      aria-label={`${cameraName} ${expanded ? "축소" : "확대"}`}
      onClick={onToggleExpand}
      onKeyDown={(e) => {
        if (e.key === "Enter" || e.key === " ") {
          e.preventDefault();
          onToggleExpand();
        }
      }}
    >
      <video
        ref={videoRef}
        className="camera-video"
        aria-label={`${cameraName} 영상`}
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
          aria-label={`${cameraName} 장비 재시작`}
          title="장비 재시작"
        >
          &#x21BB;
        </button>
      )}
      {disconnected && (
        <div className="camera-disconnected">
          <span className="disconnected-icon">&#x26A0;</span>
          <span>연결 끊김</span>
          {error && (
            <button
              className="camera-retry-btn"
              onClick={handleRetry}
              aria-label="다시 시도"
            >
              재시도
            </button>
          )}
        </div>
      )}
    </div>
  );
}
