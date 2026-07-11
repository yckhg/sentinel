/**
 * Shared JWT helpers (client-side only, no signature verification).
 *
 * JWT segments are base64url encoded (`-`/`_` instead of `+`/`/`, no `=`
 * padding). `atob()` only understands standard base64, so a payload that
 * happens to contain `-` or `_` throws InvalidCharacterError. That silently
 * made isTokenExpired return true (forced logout) and isAdmin return false
 * (admin controls hidden). We normalise to standard base64 first.
 */
export function decodeJwtPayload(token: string): Record<string, unknown> | null {
  const parts = token.split(".");
  if (parts.length !== 3) return null;
  const segment = parts[1];
  if (!segment) return null;
  try {
    let b64 = segment.replace(/-/g, "+").replace(/_/g, "/");
    const remainder = b64.length % 4;
    if (remainder === 2) b64 += "==";
    else if (remainder === 3) b64 += "=";
    else if (remainder === 1) return null; // not a valid base64url length
    const json = atob(b64);
    const parsed: unknown = JSON.parse(json);
    if (typeof parsed !== "object" || parsed === null) return null;
    return parsed as Record<string, unknown>;
  } catch {
    return null;
  }
}

/**
 * Returns true if the token is expired or malformed.
 */
export function isTokenExpired(token: string): boolean {
  const payload = decodeJwtPayload(token);
  if (!payload) return true;
  const exp = payload.exp;
  if (typeof exp !== "number") return true;
  // exp is in seconds
  return exp * 1000 < Date.now();
}

/**
 * Returns true if the token belongs to an admin. Accepts null so callers can
 * pass `localStorage.getItem("token")` directly.
 */
export function isAdmin(token: string | null): boolean {
  if (!token) return false;
  const payload = decodeJwtPayload(token);
  return payload?.role === "admin";
}
