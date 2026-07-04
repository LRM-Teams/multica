import { describe, expect, it } from "vitest";
import type { Workspace } from "../types";
import { paths } from "./paths";
import {
  chooseDefaultWorkspace,
  chooseWorkspaceDestination,
  resolvePostAuthDestination,
} from "./resolve";

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
      paths.workspace("beta").issues(),
    );
  });

  it("preferred wins even when a different workspace sorts first", () => {
    const ws = [makeWs("empty-first"), makeWs("mine")];
    expect(chooseWorkspaceDestination(ws, true, "mine")).toBe(
      paths.workspace("mine").issues(),
    );
  });

  it("preferred slug not in the accessible list → falls back to default", () => {
    const ws = [makeWs("acme"), makeWs("beta")];
    expect(chooseWorkspaceDestination(ws, true, "gone")).toBe(
      paths.workspace("acme").issues(),
    );
  });

  it("no preferred slug → default workspace", () => {
    const ws = [makeWs("acme"), makeWs("beta")];
    expect(chooseWorkspaceDestination(ws, true, null)).toBe(
      paths.workspace("acme").issues(),
    );
    expect(chooseWorkspaceDestination(ws, true)).toBe(
      paths.workspace("acme").issues(),
    );
  });

  it("onboarded + no workspace → /workspaces/new", () => {
    expect(chooseWorkspaceDestination([], true, "acme")).toBe(paths.newWorkspace());
  });
});
