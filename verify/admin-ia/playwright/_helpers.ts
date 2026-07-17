import { expect, type Page, type APIRequestContext } from "@playwright/test";

// ---------------------------------------------------------------------------
// Shared helpers for the admin-IA isolated harness (adminverify stack).
//
// Future A~I gate specs and per-page behavior specs import from here so the login
// flow and the non-admin user seeding flow live in exactly one place. This file
// defines NO tests — it is helpers + env constants only.
// ---------------------------------------------------------------------------

// Env constants with adminverify defaults (mirrors docker-compose.adminverify.yml).
export const FRONTEND = process.env.FRONTEND_URL || "http://web-frontend";
export const BACKEND = process.env.BACKEND_URL || "http://web-backend:8080";
export const ADMIN_USERNAME = process.env.ADMIN_USERNAME || "admin";
export const ADMIN_PASSWORD = process.env.ADMIN_PASSWORD || "adminverify-admin-pw";
export const INTERNAL_TOKEN = process.env.INTERNAL_TOKEN || "adminverify-internal-token";

/**
 * Browser login. Fills the login form, submits, waits for the JWT to land in
 * localStorage, and returns it. Selectors are copied verbatim from the proven
 * device-mgmt harness (device-reappear.spec.ts): #login-username / #login-password
 * / button.login-submit.
 *
 * @returns the stored JWT (throws via expect if login did not persist a token).
 */
export async function login(
  page: Page,
  username: string = ADMIN_USERNAME,
  password: string = ADMIN_PASSWORD
): Promise<string> {
  await page.goto(`${FRONTEND}/login`);
  await page.fill("#login-username", username);
  await page.fill("#login-password", password);
  await page.locator("button.login-submit").click();
  await page.waitForFunction(() => !!localStorage.getItem("token"), null, {
    timeout: 20_000,
  });
  const token = await page.evaluate(() => localStorage.getItem("token"));
  expect(token, "JWT should be stored after login").toBeTruthy();
  return token as string;
}

export interface NonAdminUserSpec {
  username: string;
  password: string;
}

export interface NonAdminUser {
  id: number;
  username: string;
  password: string;
}

/**
 * Seed an active, non-admin user via the real register→admin-approve flow, so gate
 * assertions that need a non-admin actor (e.g. E) have one that can actually log in.
 *
 * Flow (endpoints confirmed against services/web-backend/auth.go + main.go):
 *   1. POST /auth/register {username,password,confirmPassword,name} → 201, a new
 *      *pending* user (default role "user"). Password must be >= 8 chars and confirm
 *      must match.
 *   2. GET  /auth/pending  (Bearer adminToken) → [{id,username,...}] — locate ours.
 *   3. POST /auth/approve/{id} (Bearer adminToken) → user becomes status "active".
 *
 * Idempotent-safe: pass a unique username (Date.now()-based suffixes are fine in a
 * spec helper) so reruns never collide on the users.username UNIQUE constraint.
 *
 * @returns {id, username, password} for the now-active non-admin user.
 */
export async function createNonAdminUser(
  request: APIRequestContext,
  adminToken: string,
  { username, password }: NonAdminUserSpec
): Promise<NonAdminUser> {
  // 1) Register — new pending user (role defaults to "user").
  const regResp = await request.post(`${BACKEND}/auth/register`, {
    headers: { "Content-Type": "application/json" },
    data: {
      username,
      password,
      confirmPassword: password,
      name: username,
    },
  });
  expect(
    regResp.status(),
    `POST /auth/register for ${username} → 201 (got ${regResp.status()})`
  ).toBe(201);
  const registered = (await regResp.json()) as { id: number; status: string };

  // 2) Locate the pending user (admin-scoped). Prefer the registered id; fall back
  //    to a username match in the pending list.
  const pendingResp = await request.get(`${BACKEND}/auth/pending`, {
    headers: { Authorization: `Bearer ${adminToken}` },
  });
  expect(pendingResp.ok(), "GET /auth/pending → 2xx").toBeTruthy();
  const pending = (await pendingResp.json()) as Array<{
    id: number;
    username: string;
  }>;
  const match = pending.find((u) => u.username === username);
  const userId = registered.id ?? match?.id;
  expect(userId, `pending user ${username} should be present`).toBeTruthy();

  // 3) Approve → active.
  const approveResp = await request.post(`${BACKEND}/auth/approve/${userId}`, {
    headers: { Authorization: `Bearer ${adminToken}` },
  });
  expect(
    approveResp.status(),
    `POST /auth/approve/${userId} → 200 (got ${approveResp.status()})`
  ).toBe(200);

  return { id: userId as number, username, password };
}
