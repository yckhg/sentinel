import { render, screen } from "@testing-library/react";
import { describe, it, expect, vi } from "vitest";
import LoginPage from "./LoginPage";

describe("LoginPage a11y (#100)", () => {
  it("associates labels with the id/password inputs", () => {
    render(<LoginPage onLoginSuccess={vi.fn()} />);
    expect(screen.getByLabelText("아이디")).toHaveAttribute("id", "login-username");
    expect(screen.getByLabelText("비밀번호")).toHaveAttribute("id", "login-password");
  });

  it("gives the mode tabs and the submit button distinct accessible names", () => {
    render(<LoginPage onLoginSuccess={vi.fn()} />);
    // The login-mode tab is relabelled so it no longer collides with the submit.
    expect(
      screen.getByRole("button", { name: "로그인 화면으로 전환" }),
    ).toBeInTheDocument();
    // The submit button keeps the bare "로그인" accessible name — now unique.
    expect(screen.getByRole("button", { name: "로그인" })).toHaveAttribute(
      "type",
      "submit",
    );
  });
});
