import { chooseWorkspaceDestination, pickLastActiveSlug } from "@multica/core/paths";
import type { User, Workspace } from "@multica/core/types";
import { resolveRemoteApiUrl } from "@/config/runtime-urls";

export interface AuthedContext {
  user: User;
  workspaces: Workspace[];
}

/**
 * Fetch the authenticated user + their workspaces server-to-server, forwarding
 * the given cookie header (the HttpOnly `multica_auth` session). Returns null
 * when the session is not valid — missing/expired cookie, non-2xx, or a network
 * error — so callers never mis-read a failure as authenticated. GETs need no
 * CSRF. Shared by the landing Server Component and the OAuth callback route
 * handler so both compute the destination the same way (#223).
 */
export async function fetchAuthedContext(cookieHeader: string): Promise<AuthedContext | null> {
  const base = resolveRemoteApiUrl(process.env);
  try {
    const [meRes, wsRes] = await Promise.all([
      fetch(`${base}/api/me`, { headers: { cookie: cookieHeader }, cache: "no-store" }),
      fetch(`${base}/api/workspaces`, { headers: { cookie: cookieHeader }, cache: "no-store" }),
    ]);
    if (!meRes.ok || !wsRes.ok) return null;
    const user = (await meRes.json()) as User;
    const workspaces = (await wsRes.json()) as Workspace[];
    if (!user?.id || !Array.isArray(workspaces)) return null;
    return { user, workspaces };
  } catch {
    return null;
  }
}

/**
 * The post-auth destination for an authenticated context (`next` / invite are
 * resolved by callers before this). Last-active precedence: the
 * `last_workspace_slug` cookie if accessible → the accessible workspace with the
 * newest `last_active_at` (#225 server signal, restores last-active after a
 * logout that cleared the cookie) → the deterministic default.
 */
export function postAuthDestination(ctx: AuthedContext, cookieSlug: string | null): string {
  const preferredSlug = pickLastActiveSlug(ctx.workspaces, cookieSlug);
  return chooseWorkspaceDestination(ctx.workspaces, ctx.user.onboarded_at != null, preferredSlug);
}
