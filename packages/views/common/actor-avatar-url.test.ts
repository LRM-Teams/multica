import { beforeEach, describe, expect, it, vi } from "vitest";
import {
  __resetActorAvatarOkCacheForTests,
  resolveActorAvatarUrl,
} from "./actor-avatar-url";

vi.mock("@multica/core/workspace/avatar-url", () => ({
  resolvePublicFileUrl: (url: string | null | undefined) => url ?? null,
}));

describe("resolveActorAvatarUrl (LRM-224 Option B)", () => {
  beforeEach(() => {
    __resetActorAvatarOkCacheForTests();
  });

  it("prefers the actor directory over a message hint", () => {
    expect(
      resolveActorAvatarUrl({
        actorType: "agent",
        actorId: "a-1",
        directoryUrl: "/agent-avatars/dir.jpg",
        hintUrl: "/uploads/hint.png",
      }),
    ).toBe("/agent-avatars/dir.jpg");
  });

  it("uses a hint to accelerate when the directory is empty", () => {
    expect(
      resolveActorAvatarUrl({
        actorType: "agent",
        actorId: "a-1",
        directoryUrl: null,
        hintUrl: "/uploads/hint.png",
      }),
    ).toBe("/uploads/hint.png");
  });

  it("keeps a sticky face when a later hint is missing (缺字段 ≠ 清空)", () => {
    resolveActorAvatarUrl({
      actorType: "member",
      actorId: "u-1",
      directoryUrl: null,
      hintUrl: "/uploads/first.png",
    });
    expect(
      resolveActorAvatarUrl({
        actorType: "member",
        actorId: "u-1",
        directoryUrl: null,
        hintUrl: null,
      }),
    ).toBe("/uploads/first.png");
  });

  it("seeds sticky from directory so a cold directory miss later still keeps the face", () => {
    resolveActorAvatarUrl({
      actorType: "agent",
      actorId: "a-2",
      directoryUrl: "/agent-avatars/human-02.jpg",
      hintUrl: null,
    });
    expect(
      resolveActorAvatarUrl({
        actorType: "agent",
        actorId: "a-2",
        directoryUrl: null,
        hintUrl: null,
      }),
    ).toBe("/agent-avatars/human-02.jpg");
  });

  it("scopes sticky cache by actor identity, not by display name", () => {
    resolveActorAvatarUrl({
      actorType: "agent",
      actorId: "a-1",
      hintUrl: "/uploads/a1.png",
    });
    expect(
      resolveActorAvatarUrl({
        actorType: "agent",
        actorId: "a-2",
        hintUrl: null,
      }),
    ).toBeUndefined();
  });
});
