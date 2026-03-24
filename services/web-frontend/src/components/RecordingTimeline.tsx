import { useCallback, useEffect, useRef, useState } from "react";
import { fetchWithTimeout } from "../utils/fetchWithTimeout";

interface TimeRange {
  start: string;
  end: string;
}

interface ArchiveInfo {
  id: string;
  incidentId: string;
  streamKey: string;
  from: string;
  to: string;
  createdAt: string;
  sizeBytes: number;
  filePath: string;
  status: string;
  error?: string;
}

interface IncidentMarker {
  id: number;
  occurredAt: string;
  description: string;
  isTest: boolean;
}

interface RecordingTimelineProps {
  streamKey: string;
  onPlaybackRequest: (url: string | null) => void;
  isPlaying: boolean;
}

function formatTime(date: Date): string {
  return date.toLocaleTimeString("ko-KR", {
    hour: "2-digit",
    minute: "2-digit",
    hour12: false,
  });
}

function formatTimeWithSec(date: Date): string {
  return date.toLocaleTimeString("ko-KR", {
    hour: "2-digit",
    minute: "2-digit",
    second: "2-digit",
    hour12: false,
  });
}

function formatDuration(from: Date, to: Date): string {
  const diffMs = to.getTime() - from.getTime();
  const mins = Math.floor(diffMs / 60000);
  const secs = Math.floor((diffMs % 60000) / 1000);
  if (mins > 0) return `${mins}분 ${secs}초`;
  return `${secs}초`;
}

function formatBytes(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
  return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
}

export default function RecordingTimeline({ streamKey, onPlaybackRequest, isPlaying }: RecordingTimelineProps) {
  const [timeRanges, setTimeRanges] = useState<TimeRange[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  // Range selection state (0..1 fractions of the timeline window)
  const [selStart, setSelStart] = useState(0.7);
  const [selEnd, setSelEnd] = useState(1.0);
  const [dragging, setDragging] = useState<"start" | "end" | "range" | null>(null);
  const dragStartX = useRef(0);
  const dragStartVal = useRef({ start: 0, end: 0 });

  // Playback cursor position (fraction)
  const [playbackPosition, setPlaybackPosition] = useState<number | null>(null);

  // Archive state
  const [archiving, setArchiving] = useState(false);
  const [archiveResult, setArchiveResult] = useState<{
    success: boolean;
    message: string;
    archiveId?: string;
    sizeBytes?: number;
  } | null>(null);

  // Archives list
  const [archives, setArchives] = useState<ArchiveInfo[]>([]);
  const [showArchives, setShowArchives] = useState(false);
  const [downloading, setDownloading] = useState<string | null>(null);

  // Incident markers
  const [incidents, setIncidents] = useState<IncidentMarker[]>([]);

  // Timeline window: last 1 hour
  const windowEnd = useRef(new Date());
  const windowStart = useRef(new Date(windowEnd.current.getTime() - 60 * 60 * 1000));

  const timelineRef = useRef<HTMLDivElement>(null);

  const token = localStorage.getItem("token");

  const fetchTimeRanges = useCallback(async () => {
    try {
      const res = await fetchWithTimeout(`/api/recordings/${streamKey}`, {
        headers: token ? { Authorization: `Bearer ${token}` } : {},
      });
      if (res.ok) {
        const data = await res.json();
        setTimeRanges(data.timeRanges || []);
        setError(null);
      } else if (res.status === 404) {
        setTimeRanges([]);
        setError(null);
      } else {
        setError("녹화 정보를 불러올 수 없습니다");
      }
    } catch {
      setError("녹화 서비스에 연결할 수 없습니다");
    } finally {
      setLoading(false);
    }
  }, [streamKey, token]);

  const fetchArchives = useCallback(async () => {
    try {
      const res = await fetchWithTimeout("/api/archives", {
        headers: token ? { Authorization: `Bearer ${token}` } : {},
      });
      if (res.ok) {
        const data: ArchiveInfo[] = await res.json();
        setArchives(data.filter((a) => a.streamKey === streamKey));
      }
    } catch {
      // silent
    }
  }, [streamKey, token]);

  const fetchIncidents = useCallback(async () => {
    try {
      const from = windowStart.current.toISOString();
      const to = windowEnd.current.toISOString();
      const res = await fetchWithTimeout(`/api/incidents?from=${encodeURIComponent(from)}&to=${encodeURIComponent(to)}&limit=100`, {
        headers: token ? { Authorization: `Bearer ${token}` } : {},
      });
      if (res.ok) {
        const data = await res.json();
        setIncidents(data.incidents || []);
      }
    } catch {
      // silent — incidents are optional decoration
    }
  }, [token]);

  useEffect(() => {
    fetchTimeRanges();
    fetchArchives();
    fetchIncidents();
  }, [fetchTimeRanges, fetchArchives, fetchIncidents]);

  // Convert fraction to absolute time
  const fractionToTime = (frac: number): Date => {
    const start = windowStart.current.getTime();
    const end = windowEnd.current.getTime();
    return new Date(start + frac * (end - start));
  };

  // Convert absolute time to fraction
  const timeToFraction = (time: Date): number => {
    const start = windowStart.current.getTime();
    const end = windowEnd.current.getTime();
    return Math.max(0, Math.min(1, (time.getTime() - start) / (end - start)));
  };

  // Pointer event handlers for drag
  const handlePointerDown = (e: React.PointerEvent, handle: "start" | "end" | "range") => {
    e.preventDefault();
    e.stopPropagation();
    setDragging(handle);
    dragStartX.current = e.clientX;
    dragStartVal.current = { start: selStart, end: selEnd };
    (e.target as HTMLElement).setPointerCapture(e.pointerId);
  };

  const handlePointerMove = (e: React.PointerEvent) => {
    if (!dragging || !timelineRef.current) return;
    e.preventDefault();

    const dx = e.clientX - dragStartX.current;
    const dFrac = dx / timelineRef.current.clientWidth;

    if (dragging === "start") {
      const newStart = Math.max(0, Math.min(selEnd - 0.01, dragStartVal.current.start + dFrac));
      setSelStart(newStart);
    } else if (dragging === "end") {
      const newEnd = Math.max(selStart + 0.01, Math.min(1, dragStartVal.current.end + dFrac));
      setSelEnd(newEnd);
    } else if (dragging === "range") {
      const rangeWidth = dragStartVal.current.end - dragStartVal.current.start;
      let newStart = dragStartVal.current.start + dFrac;
      let newEnd = dragStartVal.current.end + dFrac;
      if (newStart < 0) {
        newStart = 0;
        newEnd = rangeWidth;
      }
      if (newEnd > 1) {
        newEnd = 1;
        newStart = 1 - rangeWidth;
      }
      setSelStart(newStart);
      setSelEnd(newEnd);
    }
  };

  const handlePointerUp = () => {
    setDragging(null);
  };

  // Click on timeline bar to seek to that position for playback
  const handleTimelineClick = (e: React.MouseEvent) => {
    if (dragging) return; // Don't seek while dragging handles
    if (!timelineRef.current) return;

    const rect = timelineRef.current.getBoundingClientRect();
    const frac = Math.max(0, Math.min(1, (e.clientX - rect.left) / rect.width));
    const clickTime = fractionToTime(frac);

    // Play from clicked time to end of available window (5 min block or end of recording)
    const playEnd = new Date(Math.min(
      clickTime.getTime() + 5 * 60 * 1000, // 5 minutes from click point
      windowEnd.current.getTime()
    ));

    setPlaybackPosition(frac);

    const from = clickTime.toISOString();
    const to = playEnd.toISOString();
    const playUrl = `/api/recordings/${streamKey}/play?from=${encodeURIComponent(from)}&to=${encodeURIComponent(to)}`;
    onPlaybackRequest(playUrl);
  };

  // Return to live
  const handleGoLive = () => {
    setPlaybackPosition(null);
    onPlaybackRequest(null);
  };

  // Download archive with auth headers
  const handleDownload = async (archiveId: string) => {
    setDownloading(archiveId);
    try {
      const freshToken = localStorage.getItem("token");
      const res = await fetchWithTimeout(`/api/archives/${archiveId}/download`, {
        headers: freshToken ? { Authorization: `Bearer ${freshToken}` } : {},
      });
      if (!res.ok) {
        const data = await res.json().catch(() => ({}));
        alert(data.error || `다운로드 실패 (${res.status})`);
        return;
      }
      const blob = await res.blob();
      const url = URL.createObjectURL(blob);
      const a = document.createElement("a");
      a.href = url;
      a.download = `${archiveId}.mp4`;
      document.body.appendChild(a);
      a.click();
      document.body.removeChild(a);
      URL.revokeObjectURL(url);
    } catch {
      alert("다운로드 서비스에 연결할 수 없습니다");
    } finally {
      setDownloading(null);
    }
  };

  // Archive the selected range
  const handleArchive = async () => {
    setArchiving(true);
    setArchiveResult(null);

    const from = fractionToTime(selStart);
    const to = fractionToTime(selEnd);

    try {
      const res = await fetchWithTimeout("/api/archives", {
        method: "POST",
        headers: {
          "Content-Type": "application/json",
          ...(token ? { Authorization: `Bearer ${token}` } : {}),
        },
        body: JSON.stringify({
          streamKeys: [streamKey],
          from: from.toISOString(),
          to: to.toISOString(),
        }),
      });

      if (res.ok) {
        const data = await res.json();
        const archive = data.archives?.[0];
        setArchiveResult({
          success: true,
          message: `보관 요청 완료: ${formatTime(from)} ~ ${formatTime(to)} (${formatDuration(from, to)})`,
          archiveId: archive?.id,
        });
        // Poll for archive completion
        const pollInterval = setInterval(async () => {
          try {
            const pollRes = await fetchWithTimeout("/api/archives", {
              headers: token ? { Authorization: `Bearer ${token}` } : {},
            });
            if (pollRes.ok) {
              const data: ArchiveInfo[] = await pollRes.json();
              const myArchives = data.filter((a) => a.streamKey === streamKey);
              setArchives(myArchives);
              const target = myArchives.find((a) => a.id === archive?.id);
              if (!target || target.status === "completed" || target.status === "failed") {
                clearInterval(pollInterval);
                if (target?.status === "failed") {
                  setArchiveResult({
                    success: false,
                    message: target.error || "보관 처리 중 오류가 발생했습니다",
                  });
                }
              }
            }
          } catch {
            clearInterval(pollInterval);
          }
        }, 3000);
        // Safety: stop polling after 5 minutes
        setTimeout(() => clearInterval(pollInterval), 5 * 60 * 1000);
      } else {
        const data = await res.json().catch(() => ({}));
        setArchiveResult({
          success: false,
          message: data.error || "보관 요청에 실패했습니다",
        });
      }
    } catch {
      setArchiveResult({
        success: false,
        message: "보관 서비스에 연결할 수 없습니다",
      });
    } finally {
      setArchiving(false);
    }
  };

  if (loading) {
    return <div className="rec-timeline-container"><p className="rec-timeline-loading">녹화 정보 로딩 중...</p></div>;
  }

  if (error) {
    return <div className="rec-timeline-container"><p className="rec-timeline-error">{error}</p></div>;
  }

  const selFromTime = fractionToTime(selStart);
  const selToTime = fractionToTime(selEnd);

  return (
    <div className="rec-timeline-container" onClick={(e) => e.stopPropagation()}>
      {/* Header with title and live button */}
      <div className="rec-timeline-labels">
        <span>{formatTime(windowStart.current)}</span>
        <span className="rec-timeline-title">
          {isPlaying ? (
            <>
              녹화 재생 중
              {playbackPosition !== null && (
                <span className="rec-timeline-playback-time">
                  {" "}({formatTimeWithSec(fractionToTime(playbackPosition))})
                </span>
              )}
            </>
          ) : (
            "녹화 타임라인"
          )}
        </span>
        <span>{formatTime(windowEnd.current)}</span>
      </div>

      {/* Timeline bar */}
      <div
        className="rec-timeline-bar"
        ref={timelineRef}
        onClick={handleTimelineClick}
        onPointerMove={handlePointerMove}
        onPointerUp={handlePointerUp}
        onPointerLeave={handlePointerUp}
      >
        {/* Available recording ranges */}
        {timeRanges.map((range, i) => {
          const startFrac = timeToFraction(new Date(range.start));
          const endFrac = timeToFraction(new Date(range.end));
          return (
            <div
              key={i}
              className="rec-timeline-available"
              style={{
                left: `${startFrac * 100}%`,
                width: `${(endFrac - startFrac) * 100}%`,
              }}
            />
          );
        })}

        {/* Archived ranges */}
        {archives
          .filter((a) => a.status === "completed")
          .map((a) => {
            const startFrac = timeToFraction(new Date(a.from));
            const endFrac = timeToFraction(new Date(a.to));
            return (
              <div
                key={a.id}
                className="rec-timeline-archived"
                style={{
                  left: `${startFrac * 100}%`,
                  width: `${(endFrac - startFrac) * 100}%`,
                }}
                title={`보관됨: ${formatTime(new Date(a.from))} ~ ${formatTime(new Date(a.to))}`}
              />
            );
          })}

        {/* Incident markers */}
        {incidents.map((inc) => {
          const frac = timeToFraction(new Date(inc.occurredAt));
          if (frac <= 0 || frac >= 1) return null;
          return (
            <div
              key={inc.id}
              className={`rec-timeline-incident${inc.isTest ? " test" : ""}`}
              style={{ left: `${frac * 100}%` }}
              title={`${inc.isTest ? "[테스트] " : ""}${inc.description} (${formatTimeWithSec(new Date(inc.occurredAt))})`}
            />
          );
        })}

        {/* Playback cursor */}
        {isPlaying && playbackPosition !== null && (
          <div
            className="rec-timeline-cursor"
            style={{ left: `${playbackPosition * 100}%` }}
          />
        )}

        {/* Selection range */}
        <div
          className={`rec-timeline-selection${dragging === "range" ? " dragging" : ""}`}
          style={{
            left: `${selStart * 100}%`,
            width: `${(selEnd - selStart) * 100}%`,
          }}
          onPointerDown={(e) => handlePointerDown(e, "range")}
        />

        {/* Start handle */}
        <div
          className={`rec-timeline-handle rec-timeline-handle-start${dragging === "start" ? " dragging" : ""}`}
          style={{ left: `${selStart * 100}%` }}
          onPointerDown={(e) => handlePointerDown(e, "start")}
        />

        {/* End handle */}
        <div
          className={`rec-timeline-handle rec-timeline-handle-end${dragging === "end" ? " dragging" : ""}`}
          style={{ left: `${selEnd * 100}%` }}
          onPointerDown={(e) => handlePointerDown(e, "end")}
        />
      </div>

      {/* Playback controls / selection info */}
      <div className="rec-timeline-actions">
        <span className="rec-timeline-range-info">
          {formatTime(selFromTime)} ~ {formatTime(selToTime)} ({formatDuration(selFromTime, selToTime)})
        </span>
        <div className="rec-timeline-action-btns">
          {isPlaying && (
            <button className="rec-timeline-live-btn" onClick={handleGoLive}>
              라이브
            </button>
          )}
          <button
            className="rec-timeline-archive-btn"
            onClick={handleArchive}
            disabled={archiving}
          >
            {archiving ? "보관 중..." : "보관"}
          </button>
        </div>
      </div>

      {/* Archive result */}
      {archiveResult && (
        <div className={`rec-timeline-result ${archiveResult.success ? "success" : "error"}`}>
          <span>{archiveResult.message}</span>
          {archiveResult.success && archiveResult.archiveId && (
            <button
              className="rec-timeline-download-link"
              onClick={() => handleDownload(archiveResult.archiveId!)}
              disabled={downloading === archiveResult.archiveId}
            >
              {downloading === archiveResult.archiveId ? "다운로드 중..." : "다운로드"}
            </button>
          )}
        </div>
      )}

      {/* Archives list toggle */}
      {archives.length > 0 && (
        <div className="rec-timeline-archives">
          <button
            className="rec-timeline-archives-toggle"
            onClick={() => setShowArchives(!showArchives)}
          >
            보관 목록 ({archives.length}) {showArchives ? "▲" : "▼"}
          </button>
          {showArchives && (
            <div className="rec-timeline-archives-list">
              {archives.map((a) => (
                <div key={a.id} className="rec-timeline-archive-item">
                  <div className="rec-timeline-archive-info">
                    <span className="rec-timeline-archive-time">
                      {formatTime(new Date(a.from))} ~ {formatTime(new Date(a.to))}
                    </span>
                    <span className={`rec-timeline-archive-status ${a.status}`}>
                      {a.status === "completed"
                        ? formatBytes(a.sizeBytes)
                        : a.status === "processing"
                          ? "처리 중..."
                          : a.status === "failed"
                            ? "실패"
                            : "대기"}
                    </span>
                  </div>
                  {a.status === "completed" && (
                    <button
                      className="rec-timeline-archive-download"
                      onClick={() => handleDownload(a.id)}
                      disabled={downloading === a.id}
                    >
                      {downloading === a.id ? "..." : "다운로드"}
                    </button>
                  )}
                  {a.status === "failed" && a.error && (
                    <span className="rec-timeline-archive-error" title={a.error}>
                      {a.error}
                    </span>
                  )}
                </div>
              ))}
            </div>
          )}
        </div>
      )}
    </div>
  );
}
