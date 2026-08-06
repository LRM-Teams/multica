// @vitest-environment jsdom
import { describe, it, expect, vi, beforeEach } from "vitest";
import type { QueryClient } from "@tanstack/react-query";
import { paths } from "@multica/core/paths";
import type { Workspace } from "@multica/core/types";

const { mockListMyInvitations, mockSetQueryData } = vi.hoisted(() => ({
  mockListMyInvitations: vi.fn(),
  mockSetQueryData: vi.fn(),
}));

vi.mock("@multica/core/api", () => ({ api: { listMyInvitations: mockListMyInvitations } }));
vi.mock("@multica/core/workspace/queries", () => ({
  workspaceKeys: { myInvitations: () => ["myInvitations"] },
}));

import { resolveLoggedInDestination } from "./resolve-logged-in-destination";

function ws(slug: string, lastActiveAt: string | null = null): Workspace {
  return {
    id: slug,
    name: slug,
    slug,
    description: null,
    context: null,
    settings: {},
    issue_prefix: slug,
    avatar_url: null,
    last_active_at: lastActiveAt,
    created_at: "",
    updated_at: "",
  };
}

const qc = { setQueryData: mockSetQueryData } as unknown as QueryClient;

function clearCookies() {
  for (const c of document.cookie.split(";")) {
    const name = c.split("=")[0]?.trim();
    if (name) document.cookie = `${name}=; max-age=0; path=/`;
  }
}

beforeEach(() => {
  vi.clearAllMocks();
  clearCookies();
  mockListMyInvitations.mockResolvedValue([]);
});

describe("resolveLoggedInDestination (#223 — email/code login shares the auth destination contract)", () => {
  it("cookie missing → newest last_active_at (the logout→re-login recovery, not the [0] default)", async () => {
    // 'a' is created first (would be the deterministic default); 'b' is last-active.
    const dest = await resolveLoggedInDestination(qc, true, [ws("a", null), ws("b", "2026-01-05")]);
    expect(dest).toBe(paths.workspace("b").channels());
  });

  it("cookie accessible → the cookie wins over last_active_at", async () => {
    document.cookie = "last_workspace_slug=a; path=/";
    const dest = await resolveLoggedInDestination(qc, true, [ws("a", null), ws("b", "2026-01-05")]);
    expect(dest).toBe(paths.workspace("a").channels());
  });

  it("no cookie and no last_active → deterministic default (workspaces[0])", async () => {
    const dest = await resolveLoggedInDestination(qc, true, [ws("a"), ws("b")]);
    expect(dest).toBe(paths.workspace("a").channels());
  });

  it("un-onboarded with pending invites → /invitations", async () => {
    mockListMyInvitations.mockResolvedValue([{ id: "i1" }]);
    const dest = await resolveLoggedInDestination(qc, false, [ws("a", "2026-01-05")]);
    expect(dest).toBe(paths.invitations());
    expect(mockSetQueryData).toHaveBeenCalled();
  });

  it("un-onboarded, no invites → onboarding (parity: invite lookup only when un-onboarded)", async () => {
    const dest = await resolveLoggedInDestination(qc, false, [ws("a", null), ws("b", "2026-01-05")]);
    expect(dest).toBe(paths.onboarding());
  });

  it("onboarded never triggers the invite lookup", async () => {
    await resolveLoggedInDestination(qc, true, [ws("a", "2026-01-05")]);
    expect(mockListMyInvitations).not.toHaveBeenCalled();
  });
});
