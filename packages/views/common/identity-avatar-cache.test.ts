import { describe, expect, it, beforeEach, vi } from "vitest";
import {
  __resetIdentityAvatarOkCacheForTests,
  resolveIdentityAvatarUrl,
  rememberIdentityAvatarUrl,
} from "./identity-avatar-cache";

vi.mock("@multica/core/workspace/avatar-url", () => ({
  resolvePublicFileUrl: (url: string | null | undefined) => url ?? null,
}));

describe("identity-avatar-cache (LRM-224)", () => {
  beforeEach(() => {
    __resetIdentityAvatarOkCacheForTests();
  });

  it("seeds from hint and reuses sticky when a later hint is null", () => {
    expect(
      resolveIdentityAvatarUrl({
        actorType: "agent",
        actorId: "a-1",
        avatarUrlHint: "/uploads/face.png",
      }),
    ).toBe("/uploads/face.png");

    expect(
      resolveIdentityAvatarUrl({
        actorType: "agent",
        actorId: "a-1",
        avatarUrlHint: null,
      }),
    ).toBe("/uploads/face.png");
  });

  it("maps chat type user → member for the same sticky key", () => {
    rememberIdentityAvatarUrl("user", "u-1", "/uploads/u.png");
    expect(
      resolveIdentityAvatarUrl({
        actorType: "member",
        actorId: "u-1",
        avatarUrlHint: null,
      }),
    ).toBe("/uploads/u.png");
  });

  it("falls through to directory when sticky is empty", () => {
    expect(
      resolveIdentityAvatarUrl({
        actorType: "member",
        actorId: "u-2",
        directoryUrl: "/uploads/dir.png",
      }),
    ).toBe("/uploads/dir.png");
  });

  it("never clears sticky when hint and directory are both empty", () => {
    rememberIdentityAvatarUrl("agent", "a-2", "/uploads/keep.png");
    expect(
      resolveIdentityAvatarUrl({
        actorType: "agent",
        actorId: "a-2",
        avatarUrlHint: undefined,
        directoryUrl: null,
      }),
    ).toBe("/uploads/keep.png");
  });
});
