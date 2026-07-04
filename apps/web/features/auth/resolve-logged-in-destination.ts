import type { QueryClient } from "@tanstack/react-query";
import { chooseWorkspaceDestination, paths, pickLastActiveSlug } from "@multica/core/paths";
import { api } from "@multica/core/api";
import { workspaceKeys } from "@multica/core/workspace/queries";
import type { Workspace } from "@multica/core/types";

function readCookie(name: string): string | null {
  if (typeof document === "undefined") return null;
  const match = new RegExp(`(?:^|; )${name}=([^;]*)`).exec(document.cookie);
  return match?.[1] ? decodeURIComponent(match[1]) : null;
}

/**
 * Post-auth destination for the email/code login page. Uses the SAME precedence
 * as the landing Server Component and the OAuth callback route handler — the
 * destination is a contract shared by every auth entry, not per-entry logic
 * (#223): invite (un-onboarded only) → last-active (`last_workspace_slug` cookie
 * if accessible, else newest `last_active_at`, #225) → deterministic default.
 * Without the last-active step an email re-login dropped the user into the
 * earliest (possibly empty) workspace — the exact incident, via the login form.
 */
export async function resolveLoggedInDestination(
  qc: QueryClient,
  hasOnboarded: boolean,
  workspaces: Workspace[],
): Promise<string> {
  if (!hasOnboarded) {
    try {
      const invites = await api.listMyInvitations();
      if (invites.length > 0) {
        qc.setQueryData(workspaceKeys.myInvitations(), invites);
        return paths.invitations();
      }
    } catch {
      // Network blip on the invite lookup is non-fatal — fall through.
    }
  }
  const cookieSlug = readCookie("last_workspace_slug");
  return chooseWorkspaceDestination(
    workspaces,
    hasOnboarded,
    pickLastActiveSlug(workspaces, cookieSlug),
  );
}
