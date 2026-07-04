import type { Workspace } from "../types";
import { useAuthStore } from "../auth";
import { paths } from "./paths";

/**
 * Priority (onboarded-first):
 *   !hasOnboarded               → /onboarding
 *   hasOnboarded + workspace[0] → /<first.slug>/issues
 *   hasOnboarded + no workspace → /workspaces/new
 *
 * V3 invariant: `onboarded_at != null` is the single source of truth for
 * "may access /<slug>/*". The web workspace layout and the desktop App.tsx
 * overlay decision both gate on this — sending an un-onboarded user
 * straight to /issues would just be redirected back to /onboarding by
 * the layout gate, costing a navigation round-trip. Check onboarded
 * first.
 *
 * In v3 "has workspace but !onboarded" is physically rare (a user can
 * only land in that state by closing the app between Step 2 and Step 3
 * — both questionnaire and runtime picker steps run after workspace
 * creation but before CompleteOnboarding). OnboardingFlow's Step 2
 * already recognizes existing workspaces and offers "Continue with
 * {name}", so the recovery is seamless.
 *
 * Callers that need invitation-aware routing (callback / login) handle
 * the "un-onboarded with pending invites" branch themselves before calling
 * this resolver — this resolver only deals with the post-invite-check
 * destination.
 */
/**
 * The default workspace to land in when there is no better signal (no preferred
 * last-active slug). This is a NAMED, deterministic strategy — NOT a bare
 * `workspaces[0]` scattered inline (#223): the workspace list comes back
 * `ORDER BY created_at ASC` and carries no owner/role/joined-at field, so we
 * cannot honestly pick "most recent" or "your own". It deterministically picks
 * the first accessible workspace; when that happens to be empty, the #224
 * empty-state card is the backstop. This is the single place to improve once a
 * backend signal (per-user last-active / meaningful ordering) exists.
 */
export function chooseDefaultWorkspace(workspaces: Workspace[]): Workspace | null {
  return workspaces[0] ?? null;
}

/**
 * Pure post-auth destination chooser. Precedence (`next` / invite are resolved
 * by callers before this): last-active `preferredSlug` if still accessible →
 * `chooseDefaultWorkspace` → /workspaces/new. `!hasOnboarded` short-circuits to
 * onboarding. Kept pure (no storage/cookie reads) so it is trivially testable;
 * the server route / caller supplies `preferredSlug`.
 */
export function chooseWorkspaceDestination(
  workspaces: Workspace[],
  hasOnboarded: boolean,
  preferredSlug?: string | null,
): string {
  if (!hasOnboarded) {
    return paths.onboarding();
  }
  if (preferredSlug) {
    const preferred = workspaces.find((w) => w.slug === preferredSlug);
    if (preferred) {
      return paths.workspace(preferred.slug).issues();
    }
  }
  const fallback = chooseDefaultWorkspace(workspaces);
  if (fallback) {
    return paths.workspace(fallback.slug).issues();
  }
  return paths.newWorkspace();
}

export function resolvePostAuthDestination(
  workspaces: Workspace[],
  hasOnboarded: boolean,
): string {
  return chooseWorkspaceDestination(workspaces, hasOnboarded);
}

/**
 * Single source of truth: backed by `users.onboarded_at`, which
 * arrives with the user object on every auth response.
 */
export function useHasOnboarded(): boolean {
  return useAuthStore((s) => s.user?.onboarded_at != null);
}
