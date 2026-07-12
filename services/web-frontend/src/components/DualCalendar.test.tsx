import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, it, expect, vi } from "vitest";
import DualCalendar from "./DualCalendar";

describe("DualCalendar a11y (#91)", () => {
  it("renders day cells as real buttons with date labels", async () => {
    const user = userEvent.setup();
    render(<DualCalendar startDate="" endDate="" onSelect={vi.fn()} onReset={vi.fn()} />);

    await user.click(screen.getByText("날짜 선택"));

    const dayButtons = screen
      .getAllByRole("button")
      .filter((b) => /^\d{4}-\d{2}-\d{2}$/.test(b.getAttribute("aria-label") ?? ""));
    expect(dayButtons.length).toBeGreaterThan(20);
  });

  it("disables future dates", async () => {
    const user = userEvent.setup();
    render(<DualCalendar startDate="" endDate="" onSelect={vi.fn()} onReset={vi.fn()} />);
    await user.click(screen.getByText("날짜 선택"));

    const today = new Date().toISOString().slice(0, 10);
    const future = new Date(Date.now() + 30 * 864e5).toISOString().slice(0, 10);
    const futureBtn = screen.queryByRole("button", { name: future });
    if (futureBtn) expect(futureBtn).toBeDisabled();
    // today itself must not be disabled
    const todayBtn = screen.queryByRole("button", { name: today });
    if (todayBtn) expect(todayBtn).not.toBeDisabled();
  });
});

describe("DualCalendar dismissal (#102)", () => {
  it("closes on Escape", async () => {
    const user = userEvent.setup();
    render(<DualCalendar startDate="" endDate="" onSelect={vi.fn()} onReset={vi.fn()} />);
    await user.click(screen.getByText("날짜 선택"));
    expect(screen.getAllByText(/월$/).length).toBeGreaterThan(0); // month titles visible

    await user.keyboard("{Escape}");
    expect(screen.queryAllByText(/월$/).length).toBe(0);
  });

  it("closes on an outside click", async () => {
    const user = userEvent.setup();
    render(
      <div>
        <button>바깥</button>
        <DualCalendar startDate="" endDate="" onSelect={vi.fn()} onReset={vi.fn()} />
      </div>,
    );
    await user.click(screen.getByText("날짜 선택"));
    expect(screen.getAllByText(/월$/).length).toBeGreaterThan(0);

    await user.click(screen.getByRole("button", { name: "바깥" }));
    expect(screen.queryAllByText(/월$/).length).toBe(0);
  });
});
