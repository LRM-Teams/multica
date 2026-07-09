export type MulticaCookieKind = "auth" | "csrf" | "loggedIn" | "lastWorkspaceSlug";

const DEFAULT_COOKIE_NAMES: Record<MulticaCookieKind, string> = {
  auth: "multica_auth",
  csrf: "multica_csrf",
  loggedIn: "multica_logged_in",
  lastWorkspaceSlug: "last_workspace_slug",
};

const COOKIE_SUFFIXES: Record<MulticaCookieKind, string> = {
  auth: "auth",
  csrf: "csrf",
  loggedIn: "logged_in",
  lastWorkspaceSlug: "last_workspace_slug",
};

function sanitizeCookiePrefix(raw: string | undefined | null): string {
  const prefix = (raw ?? "").trim();
  if (!prefix || prefix === "multica") return "";
  const safe = prefix.replace(/[^A-Za-z0-9_-]/g, "_").replace(/^[_-]+|[_-]+$/g, "");
  return safe && safe !== "multica" ? safe : "";
}

type EnvCarrier = { env?: Record<string, string | undefined> };

function envCookiePrefix(): string {
  const carrier = globalThis as typeof globalThis & { process?: EnvCarrier };
  const env = carrier.process?.env;
  return sanitizeCookiePrefix(env?.MULTICA_COOKIE_PREFIX || env?.NEXT_PUBLIC_MULTICA_COOKIE_PREFIX);
}

function browserPortCookiePrefix(): string {
  if (typeof window === "undefined") return "";
  // s89 currently hosts dev and main on the same IP with different ports.
  // Browser cookies ignore ports, so give the :18090 stack its own namespace.
  return window.location.port === "18090" ? "multica_main" : "";
}

export function resolveCookiePrefix(prefix?: string | null): string {
  if (prefix !== undefined) return sanitizeCookiePrefix(prefix);
  return envCookiePrefix() || browserPortCookiePrefix();
}

export function multicaCookieName(kind: MulticaCookieKind, prefix?: string | null): string {
  const resolved = resolveCookiePrefix(prefix);
  if (!resolved) return DEFAULT_COOKIE_NAMES[kind];
  return `${resolved}_${COOKIE_SUFFIXES[kind]}`;
}
