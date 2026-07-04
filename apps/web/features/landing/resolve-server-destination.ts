// Server-only by construction: `next/headers` (cookies()/headers()) throws if
// imported into a Client Component, so this module can never leak client-side.
import { cookies, headers } from "next/headers";
import { chooseWorkspaceDestination } from "@multica/core/paths";
import type { User, Workspace } from "@multica/core/types";
import { resolveRemoteApiUrl } from "@/config/runtime-urls";

/**
 * Server-side post-auth destination for a visitor to `/`, or `null` when the
 * request is not authenticated. Runs in a Server Component so the redirect
 * happens BEFORE the landing page renders — no flash of the marketing page and
 * no client `useEffect` redirect (#223).
 *
 * Auth is read from the HttpOnly `multica_auth` session cookie, forwarded to the
 * backend server-to-server (GETs need no CSRF). Any failure — missing cookie,
 * expired session, non-2xx, or a network error — is treated as "not
 * authenticated" so a real visitor is never trapped by a mis-read. This helper
 * only READS cookies; it never writes `last_workspace_slug` (a Server Component
 * cannot set cookies) — the existing `[workspaceSlug]/layout.tsx` writes it once
 * the user lands on the chosen workspace.
 */
export async function resolveServerPostAuthDestination(): Promise<string | null> {
  const cookieStore = await cookies();
  // No session cookie → definitely unauthenticated; don't hit the backend.
  if (!cookieStore.get("multica_auth")?.value) return null;

  const cookieHeader = (await headers()).get("cookie") ?? "";
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

    // Last-active workspace the user opened (best-effort). Not restored after a
    // logout that cleared it — task #225 adds the server-side per-user signal.
    const preferredSlug = cookieStore.get("last_workspace_slug")?.value ?? null;
    return chooseWorkspaceDestination(workspaces, user.onboarded_at != null, preferredSlug);
  } catch {
    return null;
  }
}
