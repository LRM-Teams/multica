import { render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import type { ChannelMember } from "@multica/core/types";
import { ChannelMembersDialog } from "./channel-members-dialog";
import { ChannelMembersList } from "./channel-members-list";

vi.mock("../../i18n/use-t", () => ({
  useT: () => ({
    t: (selector: (r: Record<string, unknown>) => string) => {
      const resources = {
        members: {
          dialog_title: "Members",
          dialog_subtitle: "#{{name}} · {{members}} people · {{agents}} agents",
          find_members: "Find members",
          add_people: "Add people",
          add: "Add",
          in_channel: "In this channel · {{count}}",
          footer_count: "{{count}} people",
          done: "Done",
          empty: "No members",
          no_results: "No matches",
          title: "Member",
          remove: "Remove",
          remove_aria: "Remove member",
        },
        message: { agent_badge: "Agent" },
        profile_popover: {
          role: {
            owner: "Owner",
            admin: "Admin",
            member: "Member",
            agent: "Agent",
          },
        },
        dm: { send_message: "Message" },
      };
      const raw = selector(resources as never);
      return typeof raw === "string"
        ? raw
            .replace("{{name}}", "multica-frank")
            .replace("{{members}}", "1")
            .replace("{{agents}}", "5")
            .replace("{{count}}", "6")
        : String(raw);
    },
  }),
}));

vi.mock("@multica/core/identity", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@multica/core/identity")>();
  return {
    ...actual,
    resolveActorIdentityPresentation: (m: ChannelMember) => ({
      displayName: m.display_name || m.name || m.member_id,
      handle: `@${m.member_id.slice(0, 8)}`,
      handleLabel: `@${m.member_id.slice(0, 8)}`,
      showHandleLabel: true,
    }),
  };
});

vi.mock("@multica/core/workspace/avatar-url", () => ({
  resolvePublicFileUrl: (url: string | null | undefined) => url ?? null,
}));

vi.mock("../../common/actor-avatar", () => ({
  ActorAvatar: ({ actorId }: { actorId: string }) => (
    <span data-testid="actor-avatar">{actorId}</span>
  ),
}));

vi.mock("../../common/actor-profile-popover", () => ({
  ActorProfileTrigger: ({
    children,
    className,
  }: {
    children: React.ReactNode;
    className?: string;
  }) => <div className={className}>{children}</div>,
}));

vi.mock("./agent-compact-activity", () => ({
  AgentCompactActivity: ({ agentId }: { agentId: string }) => (
    <span data-testid="agent-compact-activity">{agentId}</span>
  ),
  isCompactActivityLabel: () => true,
}));

function member(
  id: string,
  name: string,
  type: "user" | "agent" = "user",
): ChannelMember {
  return {
    member_id: id,
    member_type: type,
    name,
    display_name: name,
    avatar_url: null,
    created_at: "2026-01-01T00:00:00Z",
  };
}

const manyMembers = Array.from({ length: 16 }, (_, i) =>
  member(`m-${i}`, `Person ${i}`, i % 3 === 0 ? "agent" : "user"),
);

describe("ChannelMembersDialog (LRM-650)", () => {
  it("uses a flex-1 scrollable list so a long roster can reach the bottom", () => {
    render(
      <ChannelMembersDialog
        open
        onOpenChange={() => {}}
        channelName="multica-frank"
        memberCount={11}
        agentCount={5}
        members={manyMembers}
        query=""
        onQueryChange={() => {}}
        roleForMember={(m) => (m.member_type === "agent" ? "agent" : "member")}
        canManage
        isMobile
        currentUserId="me"
        onAddPeople={() => {}}
        onRemove={() => {}}
      />,
    );

    expect(screen.getByText("Members")).toBeInTheDocument();
    const list = screen.getByTestId("channel-members-list");
    expect(list.className).toMatch(/flex-1/);
    expect(list.className).toMatch(/min-h-0/);
    expect(list.className).toMatch(/overflow-y-auto/);
    expect(list.className).toMatch(/overscroll-contain/);
    expect(list.className).not.toMatch(/max-h-\[min\(280px/);
    expect(screen.getByText("Person 15")).toBeInTheDocument();
  });

  it("uses outline Add + Done-only footer (Plan A+少字)", () => {
    render(
      <ChannelMembersDialog
        open
        onOpenChange={() => {}}
        channelName="multica-frank"
        memberCount={1}
        agentCount={0}
        members={[member("u1", "Frank")]}
        query=""
        onQueryChange={() => {}}
        roleForMember={() => "owner"}
        canManage
        isMobile={false}
        currentUserId="u1"
        onAddPeople={() => {}}
      />,
    );

    const add = screen.getByRole("button", { name: /^add$/i });
    expect(add.className).not.toMatch(/bg-brand/);
    expect(screen.getByRole("button", { name: /done/i })).toBeInTheDocument();
    expect(screen.queryByText(/6 people/i)).not.toBeInTheDocument();
    expect(screen.queryByText(/in this channel/i)).not.toBeInTheDocument();
    const popup = document.querySelector('[data-slot="dialog-content"]');
    expect(popup!.className).toMatch(/bg-card/);
  });

  it("anchors the dialog as a bottom sheet on narrow viewports (class contract)", () => {
    render(
      <ChannelMembersDialog
        open
        onOpenChange={() => {}}
        channelName="multica-frank"
        memberCount={1}
        agentCount={0}
        members={[member("u1", "Frank")]}
        query=""
        onQueryChange={() => {}}
        roleForMember={() => "owner"}
        canManage={false}
        isMobile
        currentUserId="u1"
      />,
    );

    const popup = document.querySelector('[data-slot="dialog-content"]');
    expect(popup).not.toBeNull();
    expect(popup!.className).toMatch(/max-sm:bottom-0/);
    expect(popup!.className).toMatch(/max-sm:translate-y-0/);
    expect(popup!.className).toMatch(/bg-card/);
    expect(popup!.className).toMatch(/h-\[min\(85dvh,640px\)\]/);
  });
});

describe("ChannelMembersList (LRM-650)", () => {
  it("groups People / Agents without row hairlines", () => {
    render(
      <ChannelMembersList
        members={[
          member("u-owner", "Frank", "user"),
          member("u-member", "Bob", "user"),
          member("a1", "Agent One", "agent"),
        ]}
        emptyLabel="empty"
        noResultsLabel="none"
        roleForMember={(m) => {
          if (m.member_id === "u-owner") return "owner";
          if (m.member_type === "agent") return "agent";
          return "member";
        }}
        canRemove={false}
        isMobile={false}
        currentUserId="me"
      />,
    );

    expect(screen.getByTestId("channel-members-section-people")).toHaveTextContent(
      "People · 2",
    );
    expect(screen.getByTestId("channel-members-section-agents")).toHaveTextContent(
      "Agents · 1",
    );
    expect(screen.getByTestId("agent-compact-activity")).toHaveTextContent("a1");
    expect(screen.getByTestId("member-role-label")).toHaveTextContent("Owner");
    for (const row of screen.getAllByTestId("channel-members-row")) {
      expect(row.className).not.toMatch(/border-b/);
    }
  });

  it("always shows Remove at ≥44px hit target on mobile", () => {
    render(
      <ChannelMembersList
        members={[member("a1", "Agent One", "agent")]}
        emptyLabel="empty"
        noResultsLabel="none"
        roleForMember={() => "agent"}
        canRemove
        isMobile
        currentUserId="me"
        onRemove={() => {}}
      />,
    );

    const remove = screen.getByRole("button", { name: /remove member/i });
    expect(remove.className).toMatch(/min-h-11/);
    expect(remove.className).toMatch(/opacity-100/);
  });
});
