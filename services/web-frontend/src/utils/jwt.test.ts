import { describe, it, expect } from "vitest";
import { decodeJwtPayload, isTokenExpired, isAdmin } from "./jwt";

// Build a fake JWT from a payload object, encoding the payload as base64url
// (the format real JWTs use: +/- swapped, no padding).
function makeToken(payload: Record<string, unknown>): string {
  const json = JSON.stringify(payload);
  const b64 = btoa(json)
    .replace(/\+/g, "-")
    .replace(/\//g, "_")
    .replace(/=+$/, "");
  return `header.${b64}.signature`;
}

describe("decodeJwtPayload", () => {
  it("decodes a standard payload", () => {
    const token = makeToken({ role: "admin", exp: 123 });
    expect(decodeJwtPayload(token)).toEqual({ role: "admin", exp: 123 });
  });

  it("decodes a payload whose base64url contains - and _", () => {
    // Find a payload that base64url-encodes with both - and _ characters.
    // The bytes 0xFB 0xFF encode to "-" and "_" in base64url.
    const payload = { sub: "ûÿûÿ", role: "admin", exp: 999 };
    const token = makeToken(payload);
    const b64Segment = token.split(".")[1]!;
    expect(b64Segment).toMatch(/[-_]/); // ensure the test actually exercises the path
    // atob() alone would throw InvalidCharacterError on this segment.
    expect(() => atob(b64Segment)).toThrow();
    expect(decodeJwtPayload(token)).toEqual(payload);
  });

  it("returns null for a token without 3 parts", () => {
    expect(decodeJwtPayload("only.two")).toBeNull();
    expect(decodeJwtPayload("nodots")).toBeNull();
  });

  it("returns null for a non-JSON payload", () => {
    expect(decodeJwtPayload("h.!!!!.s")).toBeNull();
  });
});

describe("isTokenExpired", () => {
  it("is false for a future exp", () => {
    const token = makeToken({ exp: Math.floor(Date.now() / 1000) + 3600 });
    expect(isTokenExpired(token)).toBe(false);
  });

  it("is true for a past exp", () => {
    const token = makeToken({ exp: Math.floor(Date.now() / 1000) - 10 });
    expect(isTokenExpired(token)).toBe(true);
  });

  it("is true for a malformed token", () => {
    expect(isTokenExpired("garbage")).toBe(true);
  });

  it("does not falsely expire a valid token with -/_ in the payload", () => {
    const token = makeToken({
      sub: "ûÿûÿ",
      exp: Math.floor(Date.now() / 1000) + 3600,
    });
    expect(isTokenExpired(token)).toBe(false);
  });
});

describe("isAdmin", () => {
  it("is true for role=admin", () => {
    expect(isAdmin(makeToken({ role: "admin" }))).toBe(true);
  });

  it("is false for a non-admin role", () => {
    expect(isAdmin(makeToken({ role: "user" }))).toBe(false);
  });

  it("is false for null / malformed", () => {
    expect(isAdmin(null)).toBe(false);
    expect(isAdmin("garbage")).toBe(false);
  });

  it("does not hide admin when the payload contains -/_", () => {
    const token = makeToken({ sub: "ûÿûÿ", role: "admin" });
    const b64Segment = token.split(".")[1]!;
    expect(b64Segment).toMatch(/[-_]/);
    expect(isAdmin(token)).toBe(true);
  });
});
