import { describe, expect, it } from "vitest";
import type { Workspace } from "../types";
import { paths } from "./paths";
import {
  chooseDefaultWorkspace,
  chooseWorkspaceDestination,
  pickLastActiveSlug,
  resolvePostAuthDestination,
} from "./resolve";

function makeWs(slug: string, lastActiveAt: string | null = null): Workspace {
  return {
    id: `id-${slug}`,
    name: slug,
    slug,
    description: null,
    context: null,
    settings: {},
    repos: [],
    issue_prefix: slug.toUpperCase(),
    avatar_url: null,
    last_active_at: lastActiveAt,
    created_at: "",
    updated_at: "",
  };
}

describe("resolvePostAuthDestination", () => {
  it("!onboarded → /onboarding (even with a workspace)", () => {
    // V3 invariant: onboarded_at is the single source of truth for
    // workspace access. A user holding workspaces but flagged !onboarded
    // (rare mid-flow state: closed app between Step 2 and Step 3) gets
    // routed to /onboarding so they can finish; the layout hard gate
    // would redirect them anyway.
    const ws = [makeWs("acme")];
    expect(resolvePostAuthDestination(ws, false)).toBe(paths.onboarding());
    expect(resolvePostAuthDestination([], false)).toBe(paths.onboarding());
  });

  it("onboarded + workspace[0] → /<first.slug>/channels", () => {
    const ws = [makeWs("acme"), makeWs("beta")];
    expect(resolvePostAuthDestination(ws, true)).toBe(
      paths.workspace("acme").channels(),
    );
  });

  it("onboarded + no workspace → /workspaces/new", () => {
    // Already-onboarded user without any workspace — usually a returning
    // user whose last workspace got deleted or who left it. They skip
    // re-onboarding and go straight to workspace creation.
    expect(resolvePostAuthDestination([], true)).toBe(paths.newWorkspace());
  });
});

describe("chooseDefaultWorkspace", () => {
  it("returns the first workspace (deterministic default)", () => {
    const ws = [makeWs("acme"), makeWs("beta")];
    expect(chooseDefaultWorkspace(ws)?.slug).toBe("acme");
  });

  it("returns null when there are no workspaces", () => {
    expect(chooseDefaultWorkspace([])).toBeNull();
  });
});

describe("chooseWorkspaceDestination", () => {
  it("!onboarded → /onboarding regardless of preferred slug", () => {
    const ws = [makeWs("acme")];
    expect(chooseWorkspaceDestination(ws, false, "acme")).toBe(paths.onboarding());
  });

  it("returns the preferred (last-active) workspace when still accessible", () => {
    // The #223 fix: land back where the user was, not whichever sorts first.
    const ws = [makeWs("acme"), makeWs("beta")];
    expect(chooseWorkspaceDestination(ws, true, "beta")).toBe(
      paths.workspace("beta").channels(),
    );
  });

  it("preferred wins even when a different workspace sorts first", () => {
    const ws = [makeWs("empty-first"), makeWs("mine")];
    expect(chooseWorkspaceDestination(ws, true, "mine")).toBe(
      paths.workspace("mine").channels(),
    );
  });

  it("preferred slug not in the accessible list → falls back to default", () => {
    const ws = [makeWs("acme"), makeWs("beta")];
    expect(chooseWorkspaceDestination(ws, true, "gone")).toBe(
      paths.workspace("acme").channels(),
    );
  });

  it("no preferred slug → default workspace", () => {
    const ws = [makeWs("acme"), makeWs("beta")];
    expect(chooseWorkspaceDestination(ws, true, null)).toBe(
      paths.workspace("acme").channels(),
    );
    expect(chooseWorkspaceDestination(ws, true)).toBe(
      paths.workspace("acme").channels(),
    );
  });

  it("onboarded + no workspace → /workspaces/new", () => {
    expect(chooseWorkspaceDestination([], true, "acme")).toBe(paths.newWorkspace());
  });
});

describe("pickLastActiveSlug (#225)", () => {
  it("prefers the cookie slug when it is an accessible workspace", () => {
    const ws = [makeWs("acme", "2026-01-02"), makeWs("beta", "2026-01-05")];
    // Cookie wins even though beta has a newer last_active_at.
    expect(pickLastActiveSlug(ws, "acme")).toBe("acme");
  });

  it("falls back to the newest last_active_at when the cookie is missing", () => {
    const ws = [makeWs("acme", "2026-01-02"), makeWs("beta", "2026-01-05")];
    // This is the logout→re-login recovery: cookie was cleared, server signal wins.
    expect(pickLastActiveSlug(ws, null)).toBe("beta");
  });

  it("falls back to the newest last_active_at when the cookie is no longer accessible", () => {
    const ws = [makeWs("acme", "2026-01-02"), makeWs("beta", "2026-01-05")];
    expect(pickLastActiveSlug(ws, "gone")).toBe("beta");
  });

  it("returns null when no workspace has a last_active_at (→ caller uses default)", () => {
    const ws = [makeWs("acme", null), makeWs("beta", null)];
    expect(pickLastActiveSlug(ws, null)).toBeNull();
  });

  it("never treats workspaces[0] as last-active (list is created_at ASC)", () => {
    // acme sorts first (earliest created) but has no activity; beta is active.
    const ws = [makeWs("acme", null), makeWs("beta", "2026-01-05")];
    expect(pickLastActiveSlug(ws, null)).toBe("beta");
    expect(pickLastActiveSlug([], null)).toBeNull();
  });
});
