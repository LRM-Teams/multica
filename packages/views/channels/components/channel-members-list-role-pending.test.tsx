import { describe, expect, it, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { ChannelMember } from "@multica/core/types";
import type { GroupMemberActions } from "@multica/core/channels";

/**
 * #832 — the role rows must be honestly unavailable, not fake-available.
 *
 * Frank set a group manager, got an info toast, and believed it had worked.
 * The role mutations don't exist yet (#1321), so promote / demote / transfer
 * render DISABLED with a persistent note. These assert the user-visible
 * contract Iris specified:
 *   - the rows cannot be activated by mouse OR keyboard, and firing nothing is
 *     asserted at the callback boundary (not just "looks dimmed");
 *   - the explanation is a real text node present BEFORE any interaction, and
 *     is referenced by each row's aria-describedby (not tooltip-only);
 *   - REMOVE is deliberately NOT in this group — it has a working endpoint, so
 *     telling the user it's "coming soon" would be the opposite lie. (Wiring it
 *     back to the real confirm flow is its own ticket, #833.)
 */

vi.mock("../../i18n", () => ({
  useT: () => ({ t: (sel: (o: unknown) => string) => sel(dict) }),
}));

vi.mock("../../common/actor-profile-popover", () => ({
  ActorProfileTrigger: ({ children }: { children?: React.ReactNode }) => <div>{children}</div>,
}));
vi.mock("../../common/actor-avatar", () => ({
  ActorAvatar: () => <div data-testid="avatar" />,
}));
vi.mock("./agent-compact-activity", () => ({
  AgentCompactActivity: () => null,
}));

const dict = {
  members: {
    title: "Members",
    menu: {
      aria: "Member actions",
      promote_agent: "Set as group manager",
      promote_human: "Set as admin",
      demote_agent: "Remove group manager role",
      demote_human: "Remove admin role",
      transfer: "Transfer ownership",
      remove: "Remove from group",
      coming_soon: "Available once permissions are configured.",
      role_actions_pending:
        "Group role management is coming soon; member roles are not changed yet.",
    },
    role_badge: { owner: "Owner", manager_agent: "Group manager", manager_human: "Admin" },
    remove_aria: "Remove",
  },
  message: { agent_badge: "Agent" },
  profile_popover: { role: { owner: "Owner", admin: "Admin", member: "Member", agent: "Agent" } },
};

import { ChannelMembersList } from "./channel-members-list";

function member(id: string, type: "user" | "agent" = "user"): ChannelMember {
  return {
    member_type: type,
    member_id: id,
    name: id,
    display_name: id,
    avatar_url: null,
    role: "member",
  } as unknown as ChannelMember;
}

const ALL_ACTIONS: GroupMemberActions = {
  canPromoteToManager: true,
  canDemoteToMember: true,
  canTransferOwnership: true,
  canRemove: true,
};

function renderList(actions: GroupMemberActions = ALL_ACTIONS) {
  const onGroupMemberAction = vi.fn();
  render(
    <ChannelMembersList
      members={[member("bob")]}
      emptyLabel="empty"
      noResultsLabel="none"
      roleForMember={() => "member"}
      badgeForMember={() => null}
      memberMenu={() => actions}
      onGroupMemberAction={onGroupMemberAction}
      canRemove
      isMobile={false}
      currentUserId="me"
    />,
  );
  return onGroupMemberAction;
}

async function openMenu(user: ReturnType<typeof userEvent.setup>) {
  const trigger = screen.getByLabelText("Member actions");
  await user.click(trigger);
  // Assert the menu actually opened. Without this, every "…fires nothing" /
  // "…is not shown" assertion below would pass trivially on a menu that never
  // mounted — a negative assertion can't tell "gated" from "never rendered".
  await screen.findByTestId("group-member-menu-remove");
  expect(trigger).toHaveAttribute("aria-expanded", "true");
}

describe("ChannelMembersList — role rows are disabled while the write API is missing (#832)", () => {
  it.each([
    ["promote", "group-member-menu-promote"],
    ["demote", "group-member-menu-demote"],
    ["transfer", "group-member-menu-transfer"],
  ])("the %s row is disabled and a click fires nothing", async (_kind, testId) => {
    const user = userEvent.setup();
    const onGroupMemberAction = renderList();
    await openMenu(user);

    const row = screen.getByTestId(testId);
    expect(row).toHaveAttribute("aria-disabled", "true");

    // The real contract is "no request happens" — assert at the callback
    // boundary, not on styling. (`pointer-events-none` on a disabled row means
    // userEvent would refuse a normal click, so go through the DOM directly:
    // even a synthetic click must not reach the handler.)
    row.click();
    expect(onGroupMemberAction).not.toHaveBeenCalled();
  });

  it("keyboard activation (Enter / Space) on a disabled role row fires nothing", async () => {
    const user = userEvent.setup();
    const onGroupMemberAction = renderList();
    await openMenu(user);

    await user.keyboard("{ArrowDown}{Enter}");
    await user.keyboard("{ArrowDown} ");
    expect(onGroupMemberAction).not.toHaveBeenCalled();
  });

  it("the explanation is visible text before any interaction, and describes each disabled row", async () => {
    const user = userEvent.setup();
    renderList();
    await openMenu(user);

    const note = screen.getByTestId("group-member-menu-role-pending");
    expect(note).toBeVisible();
    expect(note.textContent).toContain("coming soon");

    // aria-describedby → assistive tech announces the reason with the row,
    // instead of just reporting "dimmed".
    for (const testId of [
      "group-member-menu-promote",
      "group-member-menu-demote",
      "group-member-menu-transfer",
    ]) {
      expect(screen.getByTestId(testId)).toHaveAttribute("aria-describedby", note.id);
    }
  });

  it("does NOT show the pending note when only remove is available — remove works and must not be labelled 'coming soon'", async () => {
    const user = userEvent.setup();
    renderList({
      canPromoteToManager: false,
      canDemoteToMember: false,
      canTransferOwnership: false,
      canRemove: true,
    });
    await openMenu(user);

    expect(screen.queryByTestId("group-member-menu-role-pending")).toBeNull();
    // Positive control: the menu really did render (so the assertion above
    // isn't passing merely because nothing mounted).
    expect(screen.getByText("Remove from group")).toBeInTheDocument();
  });

  it("remove stays actionable — it is not swept into the disabled group (#833 rewires it)", async () => {
    const user = userEvent.setup();
    const onGroupMemberAction = renderList();
    await openMenu(user);

    await user.click(screen.getByText("Remove from group"));
    expect(onGroupMemberAction).toHaveBeenCalledWith(expect.objectContaining({ member_id: "bob" }), "remove");
  });
});
