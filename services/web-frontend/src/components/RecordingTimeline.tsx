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

interface RecordingTimelineProps {
  streamKey: string;
}

function formatTime(date: Date): string {
  return date.toLocaleTimeString("ko-KR", {
    hour: "2-digit",
    minute: "2-digit",
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

export default function RecordingTimeline({ streamKey }: RecordingTimelineProps) {
  const [timeRanges, setTimeRanges] = useState<TimeRange[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  // Range selection state (0..1 fractions of the timeline window)
  const [selStart, setSelStart] = useState(0.7);
  const [selEnd, setSelEnd] = useState(1.0);
  const [dragging, setDragging] = useState<"start" | "end" | "range" | null>(null);
  const dragStartX = useRef(0);
  const dragStartVal = useRef({ start: 0, end: 0 });

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
        // Filter to this stream key
        setArchives(data.filter((a) => a.streamKey === streamKey));
      }
    } catch {
      // silent
    }
  }, [streamKey, token]);

  useEffect(() => {
    fetchTimeRanges();
    fetchArchives();
  }, [fetchTimeRanges, fetchArchives]);

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
        // Refresh archives list after a delay to let processing complete
        setTimeout(() => fetchArchives(), 3000);
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
      {/* Time labels */}
      <div className="rec-timeline-labels">
        <span>{formatTime(windowStart.current)}</span>
        <span className="rec-timeline-title">녹화 타임라인</span>
        <span>{formatTime(windowEnd.current)}</span>
      </div>

      {/* Timeline bar */}
      <div
        className="rec-timeline-bar"
        ref={timelineRef}
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

      {/* Selection info and archive button */}
      <div className="rec-timeline-actions">
        <span className="rec-timeline-range-info">
          {formatTime(selFromTime)} ~ {formatTime(selToTime)} ({formatDuration(selFromTime, selToTime)})
        </span>
        <button
          className="rec-timeline-archive-btn"
          onClick={handleArchive}
          disabled={archiving}
        >
          {archiving ? "보관 중..." : "보관"}
        </button>
      </div>

      {/* Archive result */}
      {archiveResult && (
        <div className={`rec-timeline-result ${archiveResult.success ? "success" : "error"}`}>
          <span>{archiveResult.message}</span>
          {archiveResult.success && archiveResult.archiveId && (
            <a
              href={`/api/archives/${archiveResult.archiveId}/download`}
              className="rec-timeline-download-link"
              target="_blank"
              rel="noopener noreferrer"
            >
              다운로드
            </a>
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
                    <a
                      href={`/api/archives/${a.id}/download`}
                      className="rec-timeline-archive-download"
                      target="_blank"
                      rel="noopener noreferrer"
                    >
                      다운로드
                    </a>
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
