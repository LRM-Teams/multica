import { describe, expect, it } from "vitest";
import type { Workspace } from "../types";
import { paths } from "./paths";
import { resolvePostAuthDestination } from "./resolve";

function makeWs(slug: string): Workspace {
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

  it("onboarded + workspace[0] → /<first.slug>/issues", () => {
    const ws = [makeWs("acme"), makeWs("beta")];
    expect(resolvePostAuthDestination(ws, true)).toBe(
      paths.workspace("acme").issues(),
    );
  });

  it("onboarded + no workspace → /workspaces/new", () => {
    // Already-onboarded user without any workspace — usually a returning
    // user whose last workspace got deleted or who left it. They skip
    // re-onboarding and go straight to workspace creation.
    expect(resolvePostAuthDestination([], true)).toBe(paths.newWorkspace());
  });

  it("onboarded + preferred slug still accessible → /<preferred.slug>/issues", () => {
    // The everyday happy path (#210): route the user back to the workspace they
    // last actively used, not whichever one sorts first.
    const ws = [makeWs("acme"), makeWs("beta")];
    expect(resolvePostAuthDestination(ws, true, "beta")).toBe(
      paths.workspace("beta").issues(),
    );
  });

  it("preferred slug wins even when a different workspace sorts first", () => {
    // "Empty" is not a resolver concern: an accessible preferred slug is
    // returned regardless of content, respecting the explicit choice — this is
    // what stops re-login from dropping the user into an empty first workspace.
    const ws = [makeWs("empty-first"), makeWs("mine")];
    expect(resolvePostAuthDestination(ws, true, "mine")).toBe(
      paths.workspace("mine").issues(),
    );
  });

  it("preferred slug no longer accessible → falls back to workspace[0]", () => {
    // Stale or cross-account persisted slug: not in the list → ignore it and
    // use the existing first-workspace fallback.
    const ws = [makeWs("acme"), makeWs("beta")];
    expect(resolvePostAuthDestination(ws, true, "gone")).toBe(
      paths.workspace("acme").issues(),
    );
  });

  it("no preferred slug → workspace[0] (unchanged legacy behaviour)", () => {
    const ws = [makeWs("acme"), makeWs("beta")];
    expect(resolvePostAuthDestination(ws, true, null)).toBe(
      paths.workspace("acme").issues(),
    );
    expect(resolvePostAuthDestination(ws, true, undefined)).toBe(
      paths.workspace("acme").issues(),
    );
  });

  it("preferred slug is ignored when !onboarded (onboarding takes priority)", () => {
    const ws = [makeWs("acme")];
    expect(resolvePostAuthDestination(ws, false, "acme")).toBe(paths.onboarding());
  });

  it("preferred slug with no accessible workspaces → /workspaces/new", () => {
    expect(resolvePostAuthDestination([], true, "acme")).toBe(paths.newWorkspace());
  });
});
