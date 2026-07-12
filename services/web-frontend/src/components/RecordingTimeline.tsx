import { useCallback, useEffect, useRef, useState } from "react";
import { fetchWithTimeout } from "../utils/fetchWithTimeout";
import { parseServerDate, formatKstClock } from "../utils/datetime";

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

// Archive status enum consumer contract (interface-web-api.md §계약8 L409,
// web-frontend 단언 R). The 6 canonical statuses are owned by recording spec;
// (a) handle all 6, (b) any out-of-enum status → 미완료(진행 중), NEVER completed
// (no download affordance), (c) `failed` surfaced as an error terminal + reason.
type ArchiveState =
  | "protecting"
  | "pending"
  | "finalizing"
  | "processing"
  | "completed"
  | "failed"
  | "unknown";

const ARCHIVE_STATE_LABEL: Record<Exclude<ArchiveState, "completed">, string> = {
  protecting: "보호 중",
  pending: "대기 중",
  finalizing: "마무리 중",
  processing: "처리 중...",
  failed: "실패",
  unknown: "미완료(진행 중)",
};

function normalizeArchiveState(status: string): ArchiveState {
  switch (status) {
    case "protecting":
    case "pending":
    case "finalizing":
    case "processing":
    case "completed":
    case "failed":
      return status;
    default:
      return "unknown";
  }
}

interface RecordingTimelineProps {
  streamKey: string;
  onPlaybackRequest: (url: string | null) => void;
  isPlaying: boolean;
}

function formatTime(date: Date): string {
  return formatKstClock(date, false);
}

function formatTimeWithSec(date: Date): string {
  return formatKstClock(date, true);
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
  const justDragged = useRef(false);

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

  // Timeline window: last 1 hour. Kept in state (not a mount-frozen ref) so it
  // can be advanced periodically / on demand — otherwise the "last hour" window
  // and its data go stale while the panel stays open (#101).
  const WINDOW_MS = 60 * 60 * 1000;
  const [windowEnd, setWindowEnd] = useState(() => new Date());
  const [windowStart, setWindowStart] = useState(() => new Date(Date.now() - WINDOW_MS));

  const refreshWindow = useCallback(() => {
    const end = new Date();
    setWindowEnd(end);
    setWindowStart(new Date(end.getTime() - WINDOW_MS));
  }, [WINDOW_MS]);

  const timelineRef = useRef<HTMLDivElement>(null);

  // Archive-completion polling handles. Kept in refs so the unmount cleanup can
  // stop them — otherwise collapsing the camera (unmounting) while an archive
  // is in flight leaves the 3s poll + 5min safety timeout running and calling
  // setState on an unmounted component (#93).
  const pollIntervalRef = useRef<ReturnType<typeof setInterval> | null>(null);
  const pollTimeoutRef = useRef<ReturnType<typeof setTimeout> | null>(null);

  const stopPolling = useCallback(() => {
    if (pollIntervalRef.current !== null) {
      clearInterval(pollIntervalRef.current);
      pollIntervalRef.current = null;
    }
    if (pollTimeoutRef.current !== null) {
      clearTimeout(pollTimeoutRef.current);
      pollTimeoutRef.current = null;
    }
  }, []);

  useEffect(() => stopPolling, [stopPolling]);

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
      const from = windowStart.toISOString();
      const to = windowEnd.toISOString();
      const res = await fetchWithTimeout(`/api/incidents?from=${encodeURIComponent(from)}&to=${encodeURIComponent(to)}&limit=100`, {
        headers: token ? { Authorization: `Bearer ${token}` } : {},
      });
      if (res.ok) {
        const data = await res.json();
        // GET /api/incidents envelope is { data: [...], pagination }, so read data.data.
        setIncidents(data.data || []);
      }
    } catch {
      // silent — incidents are optional decoration
    }
  }, [token, windowStart, windowEnd]);

  // Fetch (and re-fetch whenever the window advances — fetchIncidents depends on
  // windowStart/windowEnd, so advancing the window re-runs this effect).
  useEffect(() => {
    fetchTimeRanges();
    fetchArchives();
    fetchIncidents();
  }, [fetchTimeRanges, fetchArchives, fetchIncidents]);

  // Advance the window periodically so an open timeline keeps tracking the live
  // edge and picks up new recordings/incidents (#101).
  useEffect(() => {
    const id = setInterval(refreshWindow, WINDOW_MS / 60); // every minute
    return () => clearInterval(id);
  }, [refreshWindow, WINDOW_MS]);

  // Convert fraction to absolute time
  const fractionToTime = (frac: number): Date => {
    const start = windowStart.getTime();
    const end = windowEnd.getTime();
    return new Date(start + frac * (end - start));
  };

  // Convert absolute time to fraction
  const timeToFraction = (time: Date): number => {
    const start = windowStart.getTime();
    const end = windowEnd.getTime();
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
    if (dragging) {
      justDragged.current = true;
    }
    setDragging(null);
  };

  // Keyboard support for the selection handles (custom slider). Arrow keys nudge
  // the handle; Home/End jump to the bounds.
  const HANDLE_STEP = 0.02;
  const handleHandleKeyDown = (e: React.KeyboardEvent, handle: "start" | "end") => {
    let delta = 0;
    if (e.key === "ArrowLeft" || e.key === "ArrowDown") delta = -HANDLE_STEP;
    else if (e.key === "ArrowRight" || e.key === "ArrowUp") delta = HANDLE_STEP;
    else if (e.key === "Home") delta = -1;
    else if (e.key === "End") delta = 1;
    else return;
    e.preventDefault();
    if (handle === "start") {
      setSelStart((s) => Math.max(0, Math.min(selEnd - 0.01, s + delta)));
    } else {
      setSelEnd((en) => Math.max(selStart + 0.01, Math.min(1, en + delta)));
    }
  };

  // Click on timeline bar to seek to that position for playback
  const handleTimelineClick = (e: React.MouseEvent) => {
    if (dragging) return; // Don't seek while dragging handles
    if (justDragged.current) {
      justDragged.current = false;
      return;
    }
    if (!timelineRef.current) return;

    const rect = timelineRef.current.getBoundingClientRect();
    const frac = Math.max(0, Math.min(1, (e.clientX - rect.left) / rect.width));
    const clickTime = fractionToTime(frac);

    // Play from clicked time to end of available window (5 min block or end of recording)
    const playEnd = new Date(Math.min(
      clickTime.getTime() + 5 * 60 * 1000, // 5 minutes from click point
      windowEnd.getTime()
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
        // Poll for archive completion. Handles live in refs so the component's
        // unmount cleanup can stop them (#93).
        stopPolling();
        pollIntervalRef.current = setInterval(async () => {
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
                stopPolling();
                if (target?.status === "failed") {
                  setArchiveResult({
                    success: false,
                    message: target.error || "보관 처리 중 오류가 발생했습니다",
                  });
                }
              }
            }
          } catch {
            stopPolling();
          }
        }, 3000);
        // Safety: stop polling after 5 minutes
        pollTimeoutRef.current = setTimeout(() => stopPolling(), 5 * 60 * 1000);
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
        <span>{formatTime(windowStart)}</span>
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
        <span className="rec-timeline-window-end">
          {formatTime(windowEnd)}
          <button
            type="button"
            className="rec-timeline-refresh-btn"
            onClick={refreshWindow}
            aria-label="타임라인 새로고침"
            title="타임라인 새로고침"
          >
            &#x21BB;
          </button>
        </span>
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
          const occurred = parseServerDate(inc.occurredAt);
          if (!occurred) return null;
          const frac = timeToFraction(occurred);
          if (frac <= 0 || frac >= 1) return null;
          return (
            <div
              key={inc.id}
              className={`rec-timeline-incident${inc.isTest ? " test" : ""}`}
              style={{ left: `${frac * 100}%` }}
              title={`${inc.isTest ? "[테스트] " : ""}${inc.description} (${formatTimeWithSec(occurred)})`}
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
          role="slider"
          tabIndex={0}
          aria-label="구간 시작"
          aria-valuemin={0}
          aria-valuemax={100}
          aria-valuenow={Math.round(selStart * 100)}
          aria-valuetext={formatTimeWithSec(selFromTime)}
          onPointerDown={(e) => handlePointerDown(e, "start")}
          onKeyDown={(e) => handleHandleKeyDown(e, "start")}
        />

        {/* End handle */}
        <div
          className={`rec-timeline-handle rec-timeline-handle-end${dragging === "end" ? " dragging" : ""}`}
          style={{ left: `${selEnd * 100}%` }}
          role="slider"
          tabIndex={0}
          aria-label="구간 종료"
          aria-valuemin={0}
          aria-valuemax={100}
          aria-valuenow={Math.round(selEnd * 100)}
          aria-valuetext={formatTimeWithSec(selToTime)}
          onPointerDown={(e) => handlePointerDown(e, "end")}
          onKeyDown={(e) => handleHandleKeyDown(e, "end")}
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
              {archives.map((a) => {
                const state = normalizeArchiveState(a.status);
                const isCompleted = state === "completed";
                return (
                  <div key={a.id} className="rec-timeline-archive-item">
                    <div className="rec-timeline-archive-info">
                      <span className="rec-timeline-archive-time">
                        {formatTime(new Date(a.from))} ~ {formatTime(new Date(a.to))}
                      </span>
                      <span
                        className={`rec-timeline-archive-status ${state}`}
                        data-archive-state={state}
                      >
                        {isCompleted
                          ? formatBytes(a.sizeBytes)
                          : ARCHIVE_STATE_LABEL[state]}
                      </span>
                    </div>
                    {isCompleted && (
                      <button
                        className="rec-timeline-archive-download"
                        onClick={() => handleDownload(a.id)}
                        disabled={downloading === a.id}
                      >
                        {downloading === a.id ? "..." : "다운로드"}
                      </button>
                    )}
                    {state === "failed" && (
                      <span
                        className="rec-timeline-archive-error"
                        title={a.error || "보관 처리 중 오류가 발생했습니다"}
                      >
                        {a.error || "보관 처리 중 오류가 발생했습니다"}
                      </span>
                    )}
                  </div>
                );
              })}
            </div>
          )}
        </div>
      )}
    </div>
  );
}
