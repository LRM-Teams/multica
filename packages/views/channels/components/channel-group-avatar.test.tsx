import { describe, expect, it, vi, beforeEach } from "vitest";
import { act, render } from "@testing-library/react";
import type { ChannelMemberBrief } from "@multica/core/types";
import { __resetIdentityAvatarOkCacheForTests } from "../../common/identity-avatar-cache";
import { ChannelGroupAvatar } from "./channel-group-avatar";

vi.mock("@multica/core/api", () => ({
  api: { getBaseUrl: () => "" },
}));

vi.mock("@multica/core/workspace/avatar-url", () => ({
  resolvePublicFileUrl: (url: string | null | undefined) => url ?? null,
  isLegacyUploadsAvatarUrl: (url: string | null | undefined) =>
    !!url && (url.startsWith("/uploads/") || /\/uploads\//.test(url)),
  preferAuthorAvatarUrl: (
    incoming: string | null | undefined,
    cached: string | null | undefined,
  ) => incoming || cached || undefined,
}));

const nameById = new Map<string, string>();

vi.mock("@multica/core/workspace/hooks", () => ({
  useActorName: () => ({
    getActorName: (_type: string, id: string) => nameById.get(id) ?? id,
    getActorInitials: (_type: string, id: string) => {
      const name = nameById.get(id) ?? id;
      const c = name.trim().charAt(0);
      return c ? (/[a-z]/i.test(c) ? c.toUpperCase() : c) : "?";
    },
    getActorAvatarUrl: () => null,
  }),
}));

vi.mock("@multica/core/paths", () => ({
  useCurrentWorkspace: () => ({ id: "ws-1" }),
  useWorkspacePaths: () => ({
    agentDetail: (id: string) => `/agents/${id}`,
    memberDetail: (id: string) => `/members/${id}`,
  }),
}));

vi.mock("@multica/core/agents", () => ({
  useAgentPresenceDetail: () => ({
    availability: "online",
    workload: "idle",
    runningCount: 0,
    queuedCount: 0,
    capacity: 1,
  }),
  useAgentHealth: () => ({
    summary: undefined,
    events: undefined,
    isLoading: false,
    isError: false,
  }),
}));

vi.mock("@multica/core/agents/stores", () => ({
  useAgentPanelStore: (selector: (s: { open: (id: string) => void }) => unknown) =>
    selector({ open: vi.fn() }),
  useAgentXpBurstStore: (selector: (s: { bursts: Record<string, never> }) => unknown) =>
    selector({ bursts: {} }),
}));

vi.mock("../../common/agent-panel-context", () => ({
  useOpenAgentPanel: () => null,
}));

vi.mock("../../common/use-resolved-actor-identity", () => ({
  mentionTypeFromActorType: (actorType: string | null | undefined) =>
    actorType === "agent" ? "agent" : actorType === "member" || actorType === "user" ? "member" : null,
  useResolvedActorIdentity: () => ({ displayName: null, avatarUrl: null }),
}));

vi.mock("../../navigation", () => ({
  useNavigation: () => ({ push: vi.fn(), openInNewTab: vi.fn() }),
}));

function member(overrides: Partial<ChannelMemberBrief> = {}): ChannelMemberBrief {
  const m: ChannelMemberBrief = {
    member_type: "user",
    member_id: "m-1",
    name: "handle",
    display_name: "Display Name",
    avatar_url: "https://cdn.example.test/m-1.png",
    ...overrides,
  };
  if (m.display_name) nameById.set(m.member_id, m.display_name);
  return m;
}

describe("ChannelGroupAvatar", () => {
  beforeEach(() => {
    nameById.clear();
    __resetIdentityAvatarOkCacheForTests();
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
          ...members,
          member({ member_id: "u-2", avatar_url: "https://cdn.example.test/u-2.png" }),
        ]}
        size={40}
      />,
    );
    expect(container.querySelectorAll("img").length).toBe(2);

    rerender(<ChannelGroupAvatar members={[]} size={40} />);
    expect(container.querySelectorAll("img").length).toBe(0);
    expect(container.querySelector("svg")).toBeTruthy();
  });
});
