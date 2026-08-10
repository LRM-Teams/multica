// @vitest-environment node
import { describe, expect, it, beforeEach, vi } from "vitest";
import {
  __resetIdentityAvatarOkCacheForTests,
  resolveIdentityAvatarUrl,
  rememberIdentityAvatarUrl,
} from "./identity-avatar-cache";

vi.mock("@multica/core/workspace/avatar-url", () => ({
  resolvePublicFileUrl: (url: string | null | undefined) => url ?? null,
  isLegacyUploadsAvatarUrl: (url: string | null | undefined) =>
    !!url && (url.startsWith("/uploads/") || /\/uploads\//.test(url)),
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

  it("prefers the current directory avatar over a stale message hint", () => {
    expect(
      resolveIdentityAvatarUrl({
        actorType: "agent",
        actorId: "a-current",
        avatarUrlHint: "https://cdn.example.com/avatars/old.png",
        directoryUrl: "https://cdn.example.com/avatars/current.png",
      }),
    ).toBe("https://cdn.example.com/avatars/current.png");
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

  it("LRM-855: stale /uploads/ hint does not overwrite sticky OSS", () => {
    rememberIdentityAvatarUrl("agent", "a-3", "https://cdn.example.com/avatars/a.png");
    expect(
      resolveIdentityAvatarUrl({
        actorType: "agent",
        actorId: "a-3",
        avatarUrlHint: "/uploads/stale.png",
      }),
    ).toBe("https://cdn.example.com/avatars/a.png");
  });

  it("LRM-855: OSS hint replaces sticky /uploads/", () => {
    rememberIdentityAvatarUrl("agent", "a-4", "/uploads/old.png");
    expect(
      resolveIdentityAvatarUrl({
        actorType: "agent",
        actorId: "a-4",
        avatarUrlHint: "https://cdn.example.com/avatars/a.png",
      }),
    ).toBe("https://cdn.example.com/avatars/a.png");
  });
});
