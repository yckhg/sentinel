import { useState, useMemo } from "react";

interface DualCalendarProps {
  startDate: string; // YYYY-MM-DD
  endDate: string;   // YYYY-MM-DD
  onSelect: (start: string, end: string) => void;
  onReset: () => void;
}

function pad(n: number): string {
  return String(n).padStart(2, "0");
}

function toDateStr(y: number, m: number, d: number): string {
  return `${y}-${pad(m + 1)}-${pad(d)}`;
}

function todayStr(): string {
  const t = new Date();
  return toDateStr(t.getFullYear(), t.getMonth(), t.getDate());
}

function daysInMonth(year: number, month: number): number {
  return new Date(year, month + 1, 0).getDate();
}

function firstDayOfWeek(year: number, month: number): number {
  return new Date(year, month, 1).getDay();
}

const WEEKDAYS = ["일", "월", "화", "수", "목", "금", "토"];

function MonthCalendar({
  year,
  month,
  startDate,
  endDate,
  onDayClick,
  today,
}: {
  year: number;
  month: number;
  startDate: string;
  endDate: string;
  onDayClick: (dateStr: string) => void;
  today: string;
}) {
  const days = daysInMonth(year, month);
  const firstDay = firstDayOfWeek(year, month);
  const cells: (number | null)[] = [];

  for (let i = 0; i < firstDay; i++) cells.push(null);
  for (let d = 1; d <= days; d++) cells.push(d);

  const rows: (number | null)[][] = [];
  for (let i = 0; i < cells.length; i += 7) {
    rows.push(cells.slice(i, i + 7));
  }
  // pad last row
  if (rows.length > 0) {
    const lastRow = rows[rows.length - 1]!;
    while (lastRow.length < 7) lastRow.push(null);
  }

  return (
    <div className="dc-month">
      <div className="dc-month-title">
        {year}년 {month + 1}월
      </div>
      <div className="dc-weekdays">
        {WEEKDAYS.map((w) => (
          <div key={w} className="dc-weekday">{w}</div>
        ))}
      </div>
      <div className="dc-days">
        {rows.map((row, ri) => (
          <div key={ri} className="dc-week">
            {row.map((d, ci) => {
              if (d === null) return <div key={ci} className="dc-day dc-day-empty" />;
              const dateStr = toDateStr(year, month, d);
              const isToday = dateStr === today;
              const isStart = dateStr === startDate;
              const isEnd = dateStr === endDate;
              const inRange = startDate && endDate && dateStr > startDate && dateStr < endDate;
              const isFuture = dateStr > today;
              const isSelected = isStart || isEnd;

              let cls = "dc-day";
              if (isFuture) cls += " dc-day-disabled";
              else if (isSelected) cls += " dc-day-selected";
              else if (inRange) cls += " dc-day-in-range";
              if (isToday) cls += " dc-day-today";
              if (isStart && endDate) cls += " dc-day-range-start";
              if (isEnd && startDate) cls += " dc-day-range-end";

              return (
                <div
                  key={ci}
                  className={cls}
                  onClick={isFuture ? undefined : () => onDayClick(dateStr)}
                >
                  {d}
                </div>
              );
            })}
          </div>
        ))}
      </div>
    </div>
  );
}

export default function DualCalendar({ startDate, endDate, onSelect, onReset }: DualCalendarProps) {
  const [open, setOpen] = useState(false);
  const today = useMemo(todayStr, []);

  // Base month for left calendar — defaults to previous month
  const now = new Date();
  const defaultBaseYear = now.getMonth() === 0 ? now.getFullYear() - 1 : now.getFullYear();
  const defaultBaseMonth = now.getMonth() === 0 ? 11 : now.getMonth() - 1;
  const [baseYear, setBaseYear] = useState(defaultBaseYear);
  const [baseMonth, setBaseMonth] = useState(defaultBaseMonth);

  // Right calendar is always base + 1 month
  const rightYear = baseMonth === 11 ? baseYear + 1 : baseYear;
  const rightMonth = baseMonth === 11 ? 0 : baseMonth + 1;

  // Selection state: "none" | "start" (start picked, waiting for end)
  const [selecting, setSelecting] = useState(false);
  const [tempStart, setTempStart] = useState(startDate);

  const handlePrev = () => {
    if (baseMonth === 0) {
      setBaseYear(baseYear - 1);
      setBaseMonth(11);
    } else {
      setBaseMonth(baseMonth - 1);
    }
  };

  const handleNext = () => {
    // Don't go beyond current month on right side
    const maxYear = now.getFullYear();
    const maxMonth = now.getMonth();
    const nextRightYear = baseMonth >= 10 ? baseYear + 1 : baseYear;
    const nextRightMonth = (baseMonth + 2) % 12;
    if (nextRightYear > maxYear || (nextRightYear === maxYear && nextRightMonth > maxMonth)) return;

    if (baseMonth === 11) {
      setBaseYear(baseYear + 1);
      setBaseMonth(0);
    } else {
      setBaseMonth(baseMonth + 1);
    }
  };

  const handleDayClick = (dateStr: string) => {
    if (!selecting) {
      // First click — set start
      setTempStart(dateStr);
      setSelecting(true);
    } else {
      // Second click — set end
      let s = tempStart;
      let e = dateStr;
      if (e < s) {
        [s, e] = [e, s];
      }
      setSelecting(false);
      onSelect(s, e);
      setOpen(false);
    }
  };

  const handleReset = () => {
    setSelecting(false);
    setTempStart("");
    onReset();
    setOpen(false);
  };

  const handleToggle = () => {
    if (!open) {
      // Reset selection state when opening
      setSelecting(false);
      setTempStart(startDate);
    }
    setOpen(!open);
  };

  const displayStart = selecting ? tempStart : startDate;
  const displayEnd = selecting ? "" : endDate;

  // Format for display button
  const formatDisplay = () => {
    if (startDate && endDate) return `${startDate} ~ ${endDate}`;
    return "날짜 선택";
  };

  return (
    <div className="dc-wrapper">
      <div className="dc-trigger-row">
        <button className="dc-trigger-btn" onClick={handleToggle}>
          <span className="dc-trigger-icon">📅</span>
          <span className="dc-trigger-text">{formatDisplay()}</span>
        </button>
        {(startDate || endDate) && (
          <button className="mgmt-btn mgmt-btn-secondary dc-reset-btn" onClick={handleReset}>
            초기화
          </button>
        )}
      </div>

      {open && (
        <div className="dc-dropdown">
          {selecting && (
            <div className="dc-hint">종료일을 선택하세요</div>
          )}
          <div className="dc-nav">
            <button className="dc-nav-btn" onClick={handlePrev}>◀</button>
            <div className="dc-nav-spacer" />
            <button className="dc-nav-btn" onClick={handleNext}>▶</button>
          </div>
          <div className="dc-calendars">
            <MonthCalendar
              year={baseYear}
              month={baseMonth}
              startDate={displayStart}
              endDate={displayEnd}

              onDayClick={handleDayClick}
              today={today}
            />
            <MonthCalendar
              year={rightYear}
              month={rightMonth}
              startDate={displayStart}
              endDate={displayEnd}

              onDayClick={handleDayClick}
              today={today}
            />
          </div>
        </div>
      )}
    </div>
  );
}
