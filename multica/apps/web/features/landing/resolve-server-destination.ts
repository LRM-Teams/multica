// Server-only by construction: `next/headers` (cookies()/headers()) throws if
// imported into a Client Component, so this module can never leak client-side.
import { cookies, headers } from "next/headers";
import { fetchAuthedContext, postAuthDestination } from "@/features/auth/server-post-auth";

/**
 * Server-side post-auth destination for a visitor to `/`, or `null` when the
 * request is not authenticated. Runs in a Server Component so the redirect
 * happens BEFORE the landing page renders — no flash of the marketing page and
 * no client `useEffect` redirect (#223).
 *
 * Only READS cookies; it never writes `last_workspace_slug` (a Server Component
 * cannot set cookies) — the existing `[workspaceSlug]/layout.tsx` writes it once
 * the user lands on the chosen workspace. Last-active recovery (cookie missing →
 * server `last_active_at`) lives in the shared `postAuthDestination`.
 */
export async function resolveServerPostAuthDestination(): Promise<string | null> {
  const cookieStore = await cookies();
  // No session cookie → definitely unauthenticated; don't hit the backend.
  if (!cookieStore.get("multica_auth")?.value) return null;

  const cookieHeader = (await headers()).get("cookie") ?? "";
  const ctx = await fetchAuthedContext(cookieHeader);
  if (!ctx) return null;

  const cookieSlug = cookieStore.get("last_workspace_slug")?.value ?? null;
  return postAuthDestination(ctx, cookieSlug);
}
