/**
 * Decode a JWT and check if it's expired (client-side only, no signature verification).
 * Returns true if the token is expired or malformed.
 */
export function isTokenExpired(token: string): boolean {
  try {
    const parts = token.split(".");
    if (parts.length !== 3) return true;

    const payload = parts[1];
    if (!payload) return true;

    const decoded = JSON.parse(atob(payload)) as { exp?: number };
    if (typeof decoded.exp !== "number") return true;

    // Compare with current time (exp is in seconds)
    return decoded.exp * 1000 < Date.now();
  } catch {
    return true;
  }
}
