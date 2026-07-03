import type { Workspace } from "../types";
import { useAuthStore } from "../auth";
import { paths } from "./paths";

/**
 * Priority (onboarded-first):
 *   !hasOnboarded                          → /onboarding
 *   preferredSlug still accessible         → /<preferred.slug>/issues
 *   hasOnboarded + workspace[0]            → /<first.slug>/issues
 *   hasOnboarded + no workspace            → /workspaces/new
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
export function resolvePostAuthDestination(
  workspaces: Workspace[],
  hasOnboarded: boolean,
  preferredSlug?: string | null,
): string {
  if (!hasOnboarded) {
    return paths.onboarding();
  }
  // Prefer the workspace the user last actively opened, when it's still in their
  // accessible list — even if it's empty, because that was an explicit choice.
  // This stops re-login / recovery from dropping the user into whichever
  // workspace happens to sort first (e.g. an empty test workspace) (#210).
  //
  // We deliberately do NOT try to skip to a "first non-empty" workspace when
  // there's no preferred slug: the workspace list carries no content signal and
  // inferring emptiness would need a per-workspace query. That fallback is a
  // separate follow-up gated on a backend field.
  if (preferredSlug) {
    const preferred = workspaces.find((w) => w.slug === preferredSlug);
    if (preferred) {
      return paths.workspace(preferred.slug).issues();
    }
  }
  const first = workspaces[0];
  if (first) {
    return paths.workspace(first.slug).issues();
  }
  return paths.newWorkspace();
}

/**
 * Single source of truth: backed by `users.onboarded_at`, which
 * arrives with the user object on every auth response.
 */
export function useHasOnboarded(): boolean {
  return useAuthStore((s) => s.user?.onboarded_at != null);
}
