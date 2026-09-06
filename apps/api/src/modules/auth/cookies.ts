import type { Context } from "hono";
import { getCookie, setCookie } from "hono/cookie";
import type { RequestBoundary } from "../../../../../packages/runtime/src/request-boundary.js";
import { sessionCookieName } from "./model.js";

export function sessionToken(c: Context): string | null {
  const bearer = /^Bearer\s+(.+)$/i.exec(c.req.header("Authorization")?.trim() ?? "")?.[1]?.trim();
  if (bearer) return bearer;
  return getCookie(c, sessionCookieName)?.trim() || null;
}

export function writeSessionCookie(c: Context, boundary: RequestBoundary, token: string, maxAge: number) {
  const secure = boundary.secure(c);
  setCookie(c, sessionCookieName, token, { path: "/", httpOnly: true, secure,
    sameSite: secure && c.req.header("Origin") ? "None" : "Lax", maxAge });
}
