import { render, screen } from "@testing-library/react";
import { describe, it, expect, vi } from "vitest";
import ErrorBoundary from "./ErrorBoundary";

function Boom(): never {
  throw new Error("boom");
}

describe("ErrorBoundary (#99)", () => {
  it("renders children when there is no error", () => {
    render(
      <ErrorBoundary>
        <div>정상</div>
      </ErrorBoundary>,
    );
    expect(screen.getByText("정상")).toBeInTheDocument();
  });

  it("renders a fallback alert when a child throws", () => {
    const spy = vi.spyOn(console, "error").mockImplementation(() => {});
    render(
      <ErrorBoundary>
        <Boom />
      </ErrorBoundary>,
    );
    expect(screen.getByRole("alert")).toBeInTheDocument();
    spy.mockRestore();
  });

  it("isolates a throwing sibling from a healthy one", () => {
    const spy = vi.spyOn(console, "error").mockImplementation(() => {});
    render(
      <div>
        <ErrorBoundary label="banner">
          <div>배너 정상</div>
        </ErrorBoundary>
        <ErrorBoundary label="page">
          <Boom />
        </ErrorBoundary>
      </div>,
    );
    // The healthy boundary still renders even though the sibling failed.
    expect(screen.getByText("배너 정상")).toBeInTheDocument();
    expect(screen.getByRole("alert")).toBeInTheDocument();
    spy.mockRestore();
  });
});
