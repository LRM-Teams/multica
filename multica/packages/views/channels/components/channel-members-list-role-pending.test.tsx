import { describe, expect, it, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { ChannelMember } from "@multica/core/types";
import type { GroupMemberActions } from "@multica/core/channels";

/**
 * #832 — the role rows are real actions now.
 *
 * Frank set a group manager, got an info toast, and believed it had worked.
 * The interim fix made the three rows honestly unavailable while the mutations
 * were unbuilt (#1321); those mutations now exist, so the rows dispatch. What
 * this file guards is the user-visible contract Iris specified for the working
 * version:
 *   - each row dispatches its own action, asserted at the callback boundary
 *     with the member and the kind — not on styling;
 *   - the "coming soon" note is GONE (a disabled row explaining itself with an
 *     expired reason is the defect this replaced);
 *   - an in-flight action shows a NAMED status inside that member's row, not in
 *     the menu — Radix closes the menu on select, so a menu-only indicator is
 *     invisible for the entire time it matters;
 *   - while it is in flight that row's role items stay aria-disabled, so
 *     reopening the menu cannot issue the same change twice;
 *   - a failure renders in that member's row, and offers Retry only when
 *     retrying can help;
 *   - REMOVE is deliberately outside this group — it always had a working
 *     endpoint (#833 owns its confirm flow).
 *
 * HOW TO FLIP-VERIFY: point a row's onClick at a different kind → that
 * dispatch case goes red and the other two stay green (they discriminate the
 * kind, not merely that something fired). Drop `aria-disabled`/the guard while
 * rolePendingAction is set → the double-issue case goes red. Move the pending
 * `<output>` back inside the menu → the in-flight case goes red, because it
 * asserts before any menu interaction.
 *
 * The `dict` below does NOT derive from en.json: a key missing here renders as
 * an empty string, so the failure reads "expected …, received (nothing)" and
 * looks like a component bug. Keep it in step with the real dictionary.
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
vi.mock("@multica/core/workspace/hooks", () => ({
  useActorName: () => ({
    getMemberHonor: () => undefined,
    getAgentFleetRank: () => undefined,
  }),
}));
vi.mock("./agent-compact-activity", () => ({
  AgentCompactActivity: () => null,
}));

const dict = {
  members: {
    title: "Members",
    menu: {
      aria: "Member actions",
      // One label per action, not one per member type: Iris's ruling is that a
      // group manager is a 群管 whether the member is a human or an agent, so
      // the agent/human fork that used to live here is gone from the component,
      // the four locales, and this mock alike.
      promote: "Make group manager",
      demote: "Demote to member",
      transfer: "Transfer ownership",
      remove: "Remove from group",
      // #832 — the in-progress labels. This dictionary does NOT derive from
      // en.json, so a missing key renders as empty string rather than failing:
      // the assertion then reports "expected …, received (nothing)" and looks
      // like a component bug. Fourth instance of that trap today.
      role_busy_promote: "Making group manager…",
      role_busy_demote: "Demoting to member…",
      role_busy_transfer: "Transferring ownership…",
    },
    role_badge: { owner: "Owner", manager: "Group manager" },
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

function renderList(
  actions: GroupMemberActions = ALL_ACTIONS,
  extra: {
    roleFailureFor?: React.ComponentProps<typeof ChannelMembersList>["roleFailureFor"];
    rolePendingActionFor?: React.ComponentProps<typeof ChannelMembersList>["rolePendingActionFor"];
  } = {},
) {
  const onGroupMemberAction = vi.fn();
  render(
    <ChannelMembersList
      {...extra}
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

describe("ChannelMembersList — role rows are real actions (#832)", () => {
  // These replace the interim suite, which asserted the three rows were
  // disabled and explained by a "coming soon" note. That state was correct
  // while #1321 was unshipped; it is now expired, and a test guarding it would
  // block the correction rather than merely describe it.
  it.each([
    ["promote", "group-member-menu-promote"],
    ["demote", "group-member-menu-demote"],
    ["transfer", "group-member-menu-transfer"],
  ])("the %s row dispatches its action", async (kind, testId) => {
    const user = userEvent.setup();
    const onGroupMemberAction = renderList();
    await openMenu(user);

    await user.click(screen.getByTestId(testId));
    expect(onGroupMemberAction).toHaveBeenCalledWith(
      expect.objectContaining({ member_id: "bob" }),
      kind,
    );
  });

  it("the expired 'coming soon' note is gone — a disabled row with a stale reason is what this replaced", async () => {
    const user = userEvent.setup();
    renderList();
    await openMenu(user);
    expect(screen.queryByTestId("group-member-menu-role-pending")).toBeNull();
  });

  it("in-flight status renders in the ROW and is announced — the menu closes on select, so a menu-only indicator would be invisible", async () => {
    renderList(ALL_ACTIONS, { rolePendingActionFor: () => "promote" });
    // Visible without opening the menu, which is the whole point.
    const status = screen.getByTestId("channel-members-row-role-pending");
    expect(status).toHaveTextContent("Making group manager…");
    // `<output>` maps to role=status implicitly — assert what AT resolves.
    expect(screen.getByRole("status")).toBe(status);
    // Named, not a bare spinner: with three actions the user must know which.
    expect(status).not.toHaveTextContent("Demoting");
  });

  it("while an action is in flight the row's role items stay disabled — reopening the menu can't issue it twice", async () => {
    const user = userEvent.setup();
    const onGroupMemberAction = renderList(ALL_ACTIONS, {
      rolePendingActionFor: () => "promote",
    });
    await openMenu(user);
    for (const id of ["group-member-menu-promote", "group-member-menu-demote", "group-member-menu-transfer"]) {
      expect(screen.getByTestId(id)).toHaveAttribute("aria-disabled", "true");
      screen.getByTestId(id).click();
    }
    expect(onGroupMemberAction).not.toHaveBeenCalled();
  });

  it("a role failure renders in THAT member's row, and offers retry only when retrying can help", () => {
    const onDismiss = vi.fn();
    const onRetry = vi.fn();
    render(
      <ChannelMembersList
        members={[member("bob")]}
        emptyLabel="empty"
        noResultsLabel="none"
        roleForMember={() => "member"}
        badgeForMember={() => null}
        memberMenu={() => ALL_ACTIONS}
        onGroupMemberAction={vi.fn()}
        roleFailureFor={() => ({
          message: "Couldn't update the member's role. Please try again.",
          retryLabel: "Retry",
          dismissLabel: "Dismiss",
          onRetry,
          onDismiss,
        })}
        canRemove
        isMobile={false}
        currentUserId="me"
      />,
    );
    const notice = screen.getByTestId("channel-members-row-role-failed");
    expect(notice).toHaveTextContent("Couldn't update the member's role.");
    // Attached to the member's own row, not floated as a global banner.
    expect(notice.closest('[data-testid="channel-members-row"]')).not.toBeNull();
    screen.getByTestId("channel-members-row-role-retry").click();
    expect(onRetry).toHaveBeenCalled();
  });

  it("omits the retry button when retrying cannot help — a button that re-runs a call we know fails is worse than none", () => {
    render(
      <ChannelMembersList
        members={[member("bob")]}
        emptyLabel="empty"
        noResultsLabel="none"
        roleForMember={() => "member"}
        badgeForMember={() => null}
        memberMenu={() => ALL_ACTIONS}
        onGroupMemberAction={vi.fn()}
        roleFailureFor={() => ({
          message: "Ownership has changed; the member list has been refreshed.",
          dismissLabel: "Dismiss",
          onDismiss: vi.fn(),
        })}
        canRemove
        isMobile={false}
        currentUserId="me"
      />,
    );
    expect(screen.getByTestId("channel-members-row-role-failed")).toBeInTheDocument();
    expect(screen.queryByTestId("channel-members-row-role-retry")).toBeNull();
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
