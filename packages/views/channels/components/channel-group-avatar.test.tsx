import { describe, expect, it, vi, beforeEach } from "vitest";
import { act, render } from "@testing-library/react";
import type { ChannelMemberBrief } from "@multica/core/types";
import { ChannelGroupAvatar } from "./channel-group-avatar";
import { __resetActorAvatarOkCacheForTests } from "../../common/actor-avatar-url";

vi.mock("@multica/core/api", () => ({
  api: { getBaseUrl: () => "" },
}));

vi.mock("@multica/core/workspace/avatar-url", () => ({
  resolvePublicFileUrl: (url: string | null | undefined) => url ?? null,
}));

vi.mock("@multica/core/workspace/hooks", () => ({
  useActorName: () => ({
    getActorAvatarUrl: () => null,
    getActorName: (_type: string, _id: string, fallback?: string) => fallback ?? "Unknown",
  }),
}));

// Identity-first Avatar chrome is out of scope for mosaic layout tests —
// resolve URLs via the shared helper + UI base shell.
vi.mock("../../common/actor-avatar", async () => {
  const React = await import("react");
  const { ActorAvatar: Base } = await import("@multica/ui/components/common/actor-avatar");
  const { avatarGlyph } = await import("@multica/ui/lib/avatar-fallback");
  const { resolveActorAvatarUrl } = await import("../../common/actor-avatar-url");
  const { useActorName } = await import("@multica/core/workspace/hooks");
  return {
    ActorAvatar: ({
      actorType,
      actorId,
      avatarUrlHint,
      nameFallback,
      size,
      className,
    }: {
      actorType: string;
      actorId: string;
      avatarUrlHint?: string | null;
      nameFallback?: string;
      size?: number;
      className?: string;
    }) => {
      const { getActorName, getActorAvatarUrl } = useActorName();
      const name = getActorName(actorType, actorId, nameFallback);
      const avatarUrl = resolveActorAvatarUrl({
        actorType,
        actorId,
        directoryUrl: getActorAvatarUrl(actorType, actorId),
        hintUrl: avatarUrlHint,
      });
      return React.createElement(Base, {
        name,
        initials: avatarGlyph(name),
        avatarUrl,
        size,
        className,
        toneSeed: `${actorType}:${actorId}`,
      });
    },
  };
});

function member(overrides: Partial<ChannelMemberBrief> = {}): ChannelMemberBrief {
  return {
    member_type: "user",
    member_id: "m-1",
    name: "handle",
    display_name: "Display Name",
    avatar_url: "https://cdn.example.test/m-1.png",
    ...overrides,
  };
}

describe("ChannelGroupAvatar", () => {
  beforeEach(() => {
    __resetActorAvatarOkCacheForTests();
  });

  it("shows the neutral # glyph when there are no members", () => {
    const { container } = render(<ChannelGroupAvatar members={[]} size={40} />);
    expect(container.querySelector("svg")).toBeTruthy();
    expect(container.querySelectorAll("img").length).toBe(0);
  });

  it("renders a single full-circle avatar for one member", () => {
    const members = [member({ member_id: "u-1", avatar_url: "https://cdn.example.test/u-1.png" })];
    const { container } = render(<ChannelGroupAvatar members={members} size={40} />);
    const imgs = container.querySelectorAll("img");
    expect(imgs.length).toBe(1);
    expect(imgs[0]!.getAttribute("src")).toBe("https://cdn.example.test/u-1.png");
  });

  it("tiles a mixed human + agent pair using each member's own avatar_url (#644)", () => {
    const members = [
      member({
        member_type: "user",
        member_id: "u-1",
        display_name: "Alice",
        avatar_url: "https://cdn.example.test/alice.png",
      }),
      member({
        member_type: "agent",
        member_id: "a-1",
        display_name: "Beckham",
        avatar_url: "https://cdn.example.test/beckham.png",
      }),
    ];
    const { container } = render(<ChannelGroupAvatar members={members} size={40} />);
    const imgs = Array.from(container.querySelectorAll("img"));
    expect(imgs.length).toBe(2);
    expect(imgs.map((img) => img.getAttribute("src"))).toEqual([
      "https://cdn.example.test/alice.png",
      "https://cdn.example.test/beckham.png",
    ]);
  });

  it("tiles an all-human group the same way as mixed/all-agent", () => {
    const members = [
      member({ member_type: "user", member_id: "u-1", avatar_url: "https://cdn.example.test/u1.png" }),
      member({ member_type: "user", member_id: "u-2", avatar_url: "https://cdn.example.test/u2.png" }),
    ];
    const { container } = render(<ChannelGroupAvatar members={members} size={40} />);
    expect(container.querySelectorAll("img").length).toBe(2);
  });

  it("tiles an all-agent group the same way as mixed/all-human", () => {
    const members = [
      member({ member_type: "agent", member_id: "a-1", avatar_url: "https://cdn.example.test/a1.png" }),
      member({ member_type: "agent", member_id: "a-2", avatar_url: "https://cdn.example.test/a2.png" }),
    ];
    const { container } = render(<ChannelGroupAvatar members={members} size={40} />);
    expect(container.querySelectorAll("img").length).toBe(2);
  });

  it("falls back to initials for a member with a missing avatar_url, never a synthesized image", () => {
    const members = [
      member({ member_id: "u-1", display_name: "Alice", avatar_url: "https://cdn.example.test/alice.png" }),
      member({ member_id: "u-2", display_name: "Bob", avatar_url: null }),
    ];
    const { container, getByText } = render(<ChannelGroupAvatar members={members} size={40} />);
    expect(container.querySelectorAll("img").length).toBe(1);
    expect(getByText("B")).toBeTruthy();
  });

  it("falls back to initials for a member whose avatar image fails to load", () => {
    const members = [member({ member_id: "u-1", display_name: "Carol" })];
    const { container, getByText } = render(<ChannelGroupAvatar members={members} size={40} />);
    const img = container.querySelector("img")!;
    act(() => {
      img.dispatchEvent(new Event("error"));
    });
    expect(getByText("C")).toBeTruthy();
  });

  it("only ever shows the first 4 members, stably, and never re-introduces a 9-tile grid", () => {
    const members = Array.from({ length: 9 }, (_, i) =>
      member({
        member_id: `m-${i}`,
        display_name: `Member ${i}`,
        avatar_url: `https://cdn.example.test/m-${i}.png`,
      }),
    );
    const { container } = render(<ChannelGroupAvatar members={members} size={40} />);
    const imgs = Array.from(container.querySelectorAll("img"));
    expect(imgs.length).toBe(4);
    expect(imgs.map((img) => img.getAttribute("src"))).toEqual([
      "https://cdn.example.test/m-0.png",
      "https://cdn.example.test/m-1.png",
      "https://cdn.example.test/m-2.png",
      "https://cdn.example.test/m-3.png",
    ]);
  });

  it("recomputes when the member list changes (join/leave)", () => {
    const members = [member({ member_id: "u-1" })];
    const { container, rerender } = render(<ChannelGroupAvatar members={members} size={40} />);
    expect(container.querySelectorAll("img").length).toBe(1);

    rerender(
      <ChannelGroupAvatar
        members={[
          member({ member_id: "u-1" }),
          member({ member_id: "u-2", avatar_url: "https://cdn.example.test/u-2.png" }),
        ]}
        size={40}
      />,
    );
    expect(container.querySelectorAll("img").length).toBe(2);

    rerender(<ChannelGroupAvatar members={[]} size={40} />);
    expect(container.querySelector("svg")).toBeTruthy();
    expect(container.querySelectorAll("img").length).toBe(0);
  });
});
