import { render, screen, fireEvent } from "@testing-library/react";
import { describe, it, expect, vi } from "vitest";
import Modal from "./Modal";

describe("Modal a11y (#94)", () => {
  it("is a dialog with aria-modal and closes on Escape", () => {
    const onClose = vi.fn();
    render(
      <Modal onClose={onClose} ariaLabel="확인">
        <button>확인</button>
      </Modal>,
    );
    const dialog = screen.getByRole("dialog");
    expect(dialog).toHaveAttribute("aria-modal", "true");
    expect(dialog).toHaveAttribute("aria-label", "확인");

    fireEvent.keyDown(document, { key: "Escape" });
    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it("moves focus into the dialog and restores it on close", () => {
    const trigger = document.createElement("button");
    document.body.appendChild(trigger);
    trigger.focus();
    expect(document.activeElement).toBe(trigger);

    const { unmount } = render(
      <Modal onClose={vi.fn()} ariaLabel="x">
        <button>first</button>
      </Modal>,
    );
    expect(document.activeElement).toBe(screen.getByRole("button", { name: "first" }));

    unmount();
    expect(document.activeElement).toBe(trigger);
    trigger.remove();
  });

  it("traps Tab focus inside the dialog", () => {
    render(
      <Modal onClose={vi.fn()} ariaLabel="x">
        <button>a</button>
        <button>b</button>
      </Modal>,
    );
    const a = screen.getByRole("button", { name: "a" });
    const b = screen.getByRole("button", { name: "b" });
    b.focus();
    fireEvent.keyDown(document, { key: "Tab" });
    expect(document.activeElement).toBe(a); // wraps last -> first
  });
});
