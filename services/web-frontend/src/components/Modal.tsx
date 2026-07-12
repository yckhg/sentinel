import { useEffect, useRef, type ReactNode, type CSSProperties } from "react";

interface ModalProps {
  onClose: () => void;
  children: ReactNode;
  /** Extra class(es) appended to the dialog element. */
  className?: string;
  style?: CSSProperties;
  /** id of the element that titles the dialog (aria-labelledby). */
  labelledById?: string;
  /** Fallback accessible name when there is no visible title element. */
  ariaLabel?: string;
}

const FOCUSABLE =
  'a[href], button:not([disabled]), textarea:not([disabled]), input:not([disabled]), select:not([disabled]), [tabindex]:not([tabindex="-1"])';

/**
 * Accessible modal dialog: role="dialog" + aria-modal, Escape to close, focus
 * moved into the dialog on open and restored to the trigger on close, and a
 * Tab focus trap so keyboard/screen-reader users cannot tab out of it (#94).
 */
export default function Modal({
  onClose,
  children,
  className,
  style,
  labelledById,
  ariaLabel,
}: ModalProps) {
  const dialogRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    const dialog = dialogRef.current;
    const previouslyFocused = document.activeElement as HTMLElement | null;

    // Move focus into the dialog (first focusable, else the dialog itself).
    const focusables = dialog
      ? Array.from(dialog.querySelectorAll<HTMLElement>(FOCUSABLE))
      : [];
    (focusables[0] ?? dialog)?.focus();

    const onKeyDown = (e: KeyboardEvent) => {
      if (e.key === "Escape") {
        e.stopPropagation();
        onClose();
        return;
      }
      if (e.key !== "Tab" || !dialog) return;
      const items = Array.from(dialog.querySelectorAll<HTMLElement>(FOCUSABLE));
      if (items.length === 0) {
        e.preventDefault();
        dialog.focus();
        return;
      }
      const first = items[0]!;
      const last = items[items.length - 1]!;
      const active = document.activeElement;
      if (e.shiftKey && active === first) {
        e.preventDefault();
        last.focus();
      } else if (!e.shiftKey && active === last) {
        e.preventDefault();
        first.focus();
      }
    };

    document.addEventListener("keydown", onKeyDown, true);
    return () => {
      document.removeEventListener("keydown", onKeyDown, true);
      // Restore focus to whatever was focused before the dialog opened.
      previouslyFocused?.focus?.();
    };
  }, [onClose]);

  return (
    <div className="mgmt-modal-overlay" onClick={onClose}>
      <div
        ref={dialogRef}
        className={`mgmt-modal${className ? ` ${className}` : ""}`}
        style={style}
        role="dialog"
        aria-modal="true"
        aria-labelledby={labelledById}
        aria-label={labelledById ? undefined : ariaLabel}
        tabIndex={-1}
        onClick={(e) => e.stopPropagation()}
      >
        {children}
      </div>
    </div>
  );
}
