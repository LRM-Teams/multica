"use client";

import {
  resolveActorIdentityPresentation,
  type ActorIdentityPresentation,
} from "@multica/core/identity";
import type { ChannelMember } from "@multica/core/types";
import type {
  ChannelMemberBadge,
  GroupMemberActions,
  GroupMemberActionKind,
} from "@multica/core/channels";
import type { OpenAgentPanelFn } from "@multica/core/agents";
import { Skeleton } from "@multica/ui/components/ui/skeleton";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@multica/ui/components/ui/dropdown-menu";
import { cn } from "@multica/ui/lib/utils";
import { MessageSquare, MoreHorizontal, X } from "lucide-react";
import { useMemo, type ReactNode } from "react";
import { useActorName } from "@multica/core/workspace/hooks";
import { ActorAvatar } from "../../common/actor-avatar";
import { ActorStyledName } from "../../common/actor-styled-name";
import { ActorProfileTrigger } from "../../common/actor-profile-popover";
import { useT } from "../../i18n";
import { AgentCompactActivity } from "./agent-compact-activity";

export type MemberRoleLabel = "owner" | "admin" | "member" | "agent";

/** LRM-650 / Frank: section headers stay EN SoT (HUMANS / Agents), not i18n. */
function SectionHeader({ label, count }: { label: "HUMANS" | "Agents"; count: number }) {
  return (
    <div
      className="px-4 pb-1 pt-2 text-[11px] font-bold uppercase tracking-wide text-muted-foreground"
      data-testid={`channel-members-section-${label.toLowerCase()}`}
    >
      {label} · {count}
    </div>
  );
}

function MemberRow({
  m,
  roleForMember,
  badgeForMember,
  memberMenu,
  onGroupMemberAction,
  canRemove,
  isMobile,
  currentUserId,
  onOpenDm,
  onOpenAgent,
  onOpenMember,
  onRemove,
  dmPending,
  removeFailure,
  roleFailure,
  rolePendingAction,
}: {
  m: ChannelMember;
  /**
   * #839 — in-row record of a failed removal for THIS member (undefined when
   * there is none). Supplied per row so one member's failure can never render
   * on another's; `onRetry` must re-open the confirmation, not remove directly.
   */
  removeFailure?: {
    message: string;
    retryLabel: string;
    dismissLabel: string;
    onRetry: () => void;
    onDismiss: () => void;
  };
  /**
   * #832 — in-row record of a failed ROLE change for THIS member. Same shape and
   * same per-row rule as `removeFailure`: one member's failure must never render
   * on another's. Separate from it because both can be present at once and
   * collapsing them would drop one.
   *
   * `onRetry` is optional on purpose: for the kinds where repeating the same
   * call cannot help (the roster moved, or you were never permitted), a retry
   * button would promise something it can't deliver.
   */
  roleFailure?: {
    message: string;
    retryLabel?: string;
    dismissLabel: string;
    onRetry?: () => void;
    onDismiss: () => void;
  };
  /**
   * Which role action is in flight for THIS member.
   *
   * Radix closes the menu on select, so the in-progress state is rendered as an
   * inline row status rather than inside the menu — a menu-only indicator would
   * be invisible for the entire time it matters. The row's role items also stay
   * disabled while it runs, so reopening the menu cannot issue the same change
   * twice.
   */
  rolePendingAction?: "promote" | "demote" | "transfer" | null;
  roleForMember: (member: ChannelMember) => MemberRoleLabel;
  badgeForMember?: (member: ChannelMember) => ChannelMemberBadge | null;
  memberMenu?: (member: ChannelMember) => GroupMemberActions | null;
  onGroupMemberAction?: (member: ChannelMember, action: GroupMemberActionKind) => void;
  canRemove: boolean;
  isMobile: boolean;
  currentUserId: string;
  onOpenDm?: (member: ChannelMember) => void;
  onOpenAgent?: OpenAgentPanelFn;
  onOpenMember?: (userId: string) => void;
  onRemove?: (member: ChannelMember) => void;
  dmPending?: boolean;
}) {
  const { t } = useT("channels");
  const { getMemberHonor, getAgentFleetRank } = useActorName();
  const isAgent = m.member_type === "agent";
  // Names the action actually running rather than a bare spinner: with three
  // items in one menu, "…" leaves the user unsure which one they triggered.
  const rolePendingLabel =
    rolePendingAction === "transfer"
      ? t(($) => $.members.menu.role_busy_transfer)
      : rolePendingAction === "demote"
        ? t(($) => $.members.menu.role_busy_demote)
        : rolePendingAction === "promote"
          ? t(($) => $.members.menu.role_busy_promote)
          : null;
  const presentation: ActorIdentityPresentation = resolveActorIdentityPresentation(
    m,
    isAgent ? t(($) => $.message.agent_badge) : t(($) => $.members.title),
  );
  const roleKey = roleForMember(m);
  const showMutedRole = !isAgent && (roleKey === "owner" || roleKey === "admin");
  // The group member panel passes `badgeForMember` → show the channel/group role
  // (owner / 群管; ordinary members get no badge). Everywhere else, keep the
  // existing workspace-role label. #832: no separate human label — one role,
  // one name, for humans and agents alike.
  const groupBadge = badgeForMember?.(m) ?? null;
  const mutedRoleLabel = badgeForMember
    ? groupBadge
      ? t(($) => $.members.role_badge[groupBadge])
      : null
    : showMutedRole
      ? t(($) => $.profile_popover.role[roleKey])
      : null;
  const canDm = Boolean(onOpenDm) && (isAgent || m.member_id !== currentUserId);
  const actorType = isAgent ? "agent" : "member";
  const profileMemberType = isAgent ? "agent" : "user";
  const openAgentCapture =
    isAgent && onOpenAgent
      ? () => {
          onOpenAgent(m.member_id, {
            name: m.name,
            display_name: m.display_name,
            avatar_url: m.avatar_url ?? null,
          });
        }
      : undefined;
  const openMemberCapture =
    !isAgent && onOpenMember
      ? () => {
          onOpenMember(m.member_id);
        }
      : undefined;

  // Owner-only management menu (group settings). `memberMenu` returns the
  // available actions for this row; a non-owner viewer / own row yields no
  // actions (fail-closed in core), so the ⋯ trigger only renders for the owner.
  // When the menu is active it OWNS removal — the standalone Remove button is
  // suppressed to avoid a duplicate kick affordance.
  const menuActions = memberMenu?.(m) ?? null;
  const hasMenu =
    !!menuActions &&
    !!onGroupMemberAction &&
    (menuActions.canPromoteToManager ||
      menuActions.canDemoteToMember ||
      menuActions.canTransferOwnership ||
      menuActions.canRemove);

  const actorHonor = !isAgent ? getMemberHonor(m.member_id) : undefined;
  const actorFleet = isAgent ? getAgentFleetRank(m.member_id) : undefined;

  return (
    <div
      className="rounded-lg"
      data-testid="channel-members-row"
      data-member-type={m.member_type}
    >
      {/* #839 — the identity line keeps its own layout; a failure notice (below)
          is a second line inside the SAME row, so the failure stays attached to
          the member it belongs to rather than becoming a global banner. */}
      <div className="group flex min-h-[52px] items-center gap-2.5 rounded-lg px-2.5 py-2 hover:bg-hover">
      <ActorProfileTrigger
        memberType={profileMemberType}
        memberId={m.member_id}
        side="left"
        sideOffset={8}
        className="min-w-0 flex-1 items-center gap-2.5"
        onClickCapture={openAgentCapture ?? openMemberCapture}
      >
        <ActorAvatar
          actorType={actorType}
          actorId={m.member_id}
          avatarUrlHint={m.avatar_url}
          size={36}
          showStatusDot
          fleetRank={actorFleet?.fleet_rank}
          profileLink={false}
        />
        <div className="min-w-0 flex-1">
          <div className="flex min-w-0 items-baseline gap-1.5">
            <ActorStyledName
              displayName={presentation.displayName}
              honor={actorHonor}
              fleet={actorFleet}
              showBadges={false}
              className="text-sm font-semibold text-ink"
            />
            {mutedRoleLabel ? (
              <span
                data-testid="member-role-label"
                className="shrink-0 text-[11px] font-normal leading-none text-muted-foreground"
              >
                {mutedRoleLabel}
              </span>
            ) : null}
          </div>
          {isAgent ? (
            <AgentCompactActivity agentId={m.member_id} />
          ) : presentation.showHandleLabel && presentation.handleLabel ? (
            <div className="mt-0.5 truncate text-xs text-muted-foreground">
              {presentation.handleLabel}
            </div>
          ) : null}
        </div>
      </ActorProfileTrigger>
      {canDm && (
        <button
          type="button"
          onClick={() => onOpenDm?.(m)}
          disabled={dmPending}
          aria-label={t(($) => $.dm.send_message)}
          title={t(($) => $.dm.send_message)}
          className={cn(
            "rounded p-1.5 text-muted-foreground transition hover:text-foreground disabled:opacity-50",
            isMobile ? "opacity-100" : "opacity-0 group-hover:opacity-100",
          )}
        >
          <MessageSquare className="size-3.5" />
        </button>
      )}
      {hasMenu && menuActions && onGroupMemberAction && (
        <DropdownMenu>
          <DropdownMenuTrigger
            aria-label={t(($) => $.members.menu.aria)}
            className={cn(
              "shrink-0 rounded p-1.5 text-muted-foreground transition hover:text-foreground",
              isMobile ? "opacity-100" : "opacity-0 group-hover:opacity-100",
            )}
          >
            <MoreHorizontal className="size-4" />
          </DropdownMenuTrigger>
          <DropdownMenuContent align="end">
            {menuActions.canPromoteToManager && (
              <DropdownMenuItem
                disabled={!!rolePendingAction}
                data-testid="group-member-menu-promote"
                onClick={() => onGroupMemberAction(m, "promote")}
              >
                {t(($) => $.members.menu.promote)}
              </DropdownMenuItem>
            )}
            {menuActions.canDemoteToMember && (
              <DropdownMenuItem
                disabled={!!rolePendingAction}
                data-testid="group-member-menu-demote"
                onClick={() => onGroupMemberAction(m, "demote")}
              >
                {t(($) => $.members.menu.demote)}
              </DropdownMenuItem>
            )}
            {menuActions.canTransferOwnership && (
              <DropdownMenuItem
                disabled={!!rolePendingAction}
                data-testid="group-member-menu-transfer"
                onClick={() => onGroupMemberAction(m, "transfer")}
              >
                {t(($) => $.members.menu.transfer)}
              </DropdownMenuItem>
            )}
            {menuActions.canRemove && (
              <>
                <DropdownMenuSeparator />
                <DropdownMenuItem
                  variant="destructive"
                  data-testid="group-member-menu-remove"
                  onClick={() => onGroupMemberAction(m, "remove")}
                >
                  {t(($) => $.members.menu.remove)}
                </DropdownMenuItem>
              </>
            )}
          </DropdownMenuContent>
        </DropdownMenu>
      )}
      {canRemove && onRemove && !memberMenu && (
        <button
          type="button"
          onClick={() => onRemove(m)}
          aria-label={t(($) => $.members.remove_aria)}
          className={cn(
            "shrink-0 font-semibold text-destructive transition",
            isMobile
              ? "min-h-11 px-2 py-2.5 text-sm opacity-100"
              : "px-1.5 py-1 text-sm opacity-0 group-hover:opacity-100",
          )}
        >
          {t(($) => $.members.remove)}
        </button>
      )}
      </div>
      {/* #839 — durable in-row record of a failed removal. The toast is the
          immediate announcement and can be dismissed; this stays until the
          member is actually gone or the user clears it, so "it failed" is still
          discoverable afterwards. `重试` re-opens the named confirmation — it
          never re-fires the mutation directly (Iris: the second confirmation
          remains the destructive commitment point). */}
      {removeFailure ? (
        <output
          className="mx-2.5 mb-2 flex flex-wrap items-center gap-x-3 gap-y-1 rounded-md bg-destructive/10 px-2 py-1.5 text-xs text-destructive"
          data-testid="channel-members-row-remove-failed"
        >
          <span className="min-w-0 flex-1">{removeFailure.message}</span>
          <button
            type="button"
            onClick={removeFailure.onRetry}
            className={cn(
              "shrink-0 font-semibold underline underline-offset-2 hover:no-underline",
              isMobile && "min-h-11 px-1",
            )}
            data-testid="channel-members-row-remove-retry"
          >
            {removeFailure.retryLabel}
          </button>
          <button
            type="button"
            onClick={removeFailure.onDismiss}
            aria-label={removeFailure.dismissLabel}
            className={cn(
              "shrink-0 text-muted-foreground hover:text-foreground",
              isMobile && "min-h-11 px-1",
            )}
            data-testid="channel-members-row-remove-dismiss"
          >
            <X className="size-3.5" />
          </button>
        </output>
      ) : null}
      {/* #832 — in-progress status. Lives in the ROW, not the menu: Radix closes
          the menu on select, so a menu-only indicator would vanish exactly when
          the user needs it. `<output>` carries role=status implicitly, so the
          change is announced rather than only drawn. */}
      {rolePendingLabel ? (
        <output
          className="mx-2.5 mb-2 block px-2 py-1.5 text-xs text-muted-foreground"
          data-testid="channel-members-row-role-pending"
        >
          {rolePendingLabel}
        </output>
      ) : null}
      {/* #832 — role-change failure, same in-row treatment as the removal notice
          above so it stays attached to the member it belongs to. A separate node
          rather than a shared one: a member can hold a failed removal AND a
          failed role change, and merging them would silently drop one. */}
      {roleFailure ? (
        <output
          className="mx-2.5 mb-2 flex flex-wrap items-center gap-x-3 gap-y-1 rounded-md bg-destructive/10 px-2 py-1.5 text-xs text-destructive"
          data-testid="channel-members-row-role-failed"
        >
          <span className="min-w-0 flex-1">{roleFailure.message}</span>
          {roleFailure.onRetry && roleFailure.retryLabel ? (
            <button
              type="button"
              onClick={roleFailure.onRetry}
              className={cn(
                "shrink-0 font-semibold underline underline-offset-2 hover:no-underline",
                isMobile && "min-h-11 px-1",
              )}
              data-testid="channel-members-row-role-retry"
            >
              {roleFailure.retryLabel}
            </button>
          ) : null}
          <button
            type="button"
            onClick={roleFailure.onDismiss}
            aria-label={roleFailure.dismissLabel}
            className={cn(
              "shrink-0 text-muted-foreground hover:text-foreground",
              isMobile && "min-h-11 px-1",
            )}
            data-testid="channel-members-row-role-dismiss"
          >
            <X className="size-3.5" />
          </button>
        </output>
      ) : null}
    </div>
  );
}

/**
 * Shared Members list (LRM-211 / LRM-650) — dialog + Channel details 「成员」Tab.
 * LRM-650: HUMANS / Agents sections, no row hairlines, agent Compact Activity.
 */
export function ChannelMembersList({
  members,
  loading,
  emptyLabel,
  noResultsLabel,
  roleForMember,
  badgeForMember,
  memberMenu,
  onGroupMemberAction,
  canRemove,
  isMobile,
  currentUserId,
  onOpenDm,
  onOpenAgent,
  onOpenMember,
  onRemove,
  dmPending,
  headerSlot,
  removeFailureFor,
  roleFailureFor,
  rolePendingActionFor,
  className,
}: {
  /**
   * #839 — returns the in-row failure notice for a member, or undefined. Given
   * per member (not a single "last error") so a second failure cannot silently
   * replace an unresolved first one.
   */
  /**
   * #832 — same per-member contract as `removeFailureFor`: a role-change failure
   * belongs to one member and must never surface on another's row.
   */
  roleFailureFor?: (member: ChannelMember) => {
    message: string;
    retryLabel?: string;
    dismissLabel: string;
    onRetry?: () => void;
    onDismiss: () => void;
  } | undefined;
  /** #832 — which role action is in flight for a given member, if any. */
  rolePendingActionFor?: (member: ChannelMember) => "promote" | "demote" | "transfer" | null;
  removeFailureFor?: (member: ChannelMember) => {
    message: string;
    retryLabel: string;
    dismissLabel: string;
    onRetry: () => void;
    onDismiss: () => void;
  } | undefined;
  members: ChannelMember[];
  loading?: boolean;
  emptyLabel: string;
  noResultsLabel: string;
  roleForMember: (member: ChannelMember) => MemberRoleLabel;
  badgeForMember?: (member: ChannelMember) => ChannelMemberBadge | null;
  memberMenu?: (member: ChannelMember) => GroupMemberActions | null;
  onGroupMemberAction?: (member: ChannelMember, action: GroupMemberActionKind) => void;
  canRemove: boolean;
  /**
   * Optional content rendered at the top of the roster (above the HUMANS
   * section, inside the scroll area). The group-manager onboarding hint mounts
   * here — it self-fetches members + viewer channel role and owns its own
   * owner-only / 0-manager / dismiss logic; this component only lends the slot.
   */
  headerSlot?: ReactNode;
  isMobile: boolean;
  currentUserId: string;
  onOpenDm?: (member: ChannelMember) => void;
  onOpenAgent?: OpenAgentPanelFn;
  onOpenMember?: (userId: string) => void;
  onRemove?: (member: ChannelMember) => void;
  dmPending?: boolean;
  className?: string;
}) {
  const { humans, agents } = useMemo(() => {
    const h: ChannelMember[] = [];
    const a: ChannelMember[] = [];
    for (const m of members) {
      if (m.member_type === "agent") a.push(m);
      else h.push(m);
    }
    return { humans: h, agents: a };
  }, [members]);

  if (loading) {
    return (
      <div
        className={cn(
          "min-h-0 space-y-2 overflow-y-auto overscroll-contain px-5 py-3 [-webkit-overflow-scrolling:touch]",
          className,
        )}
        aria-busy="true"
      >
        {Array.from({ length: 4 }).map((_, i) => (
          <div key={i} className="flex items-center gap-3">
            <Skeleton className="size-9 shrink-0 rounded-full" />
            <div className="min-w-0 flex-1 space-y-1.5">
              <Skeleton className="h-3.5 w-28" />
              <Skeleton className="h-3 w-20" />
            </div>
          </div>
        ))}
      </div>
    );
  }

  if (members.length === 0) {
    return (
      <p
        className={cn(
          "min-h-0 px-5 py-10 text-center text-sm text-foreground",
          className,
        )}
      >
        {emptyLabel || noResultsLabel}
      </p>
    );
  }

  return (
    <div
      className={cn(
        "min-h-0 overflow-y-auto overscroll-contain px-2 pb-2 [-webkit-overflow-scrolling:touch]",
        className,
      )}
      data-testid="channel-members-list"
    >
      {headerSlot}
      {humans.length > 0 ? (
        <>
          <SectionHeader label="HUMANS" count={humans.length} />
          <div className="px-1 pb-1">
            {humans.map((m) => (
              <MemberRow
                key={`${m.member_type}:${m.member_id}`}
                m={m}
                roleForMember={roleForMember}
                badgeForMember={badgeForMember}
                memberMenu={memberMenu}
                onGroupMemberAction={onGroupMemberAction}
                canRemove={canRemove}
                isMobile={isMobile}
                currentUserId={currentUserId}
                onOpenDm={onOpenDm}
                onOpenAgent={onOpenAgent}
                onOpenMember={onOpenMember}
                onRemove={onRemove}
                dmPending={dmPending}
                removeFailure={removeFailureFor?.(m)}
                roleFailure={roleFailureFor?.(m)}
                rolePendingAction={rolePendingActionFor?.(m) ?? null}
              />
            ))}
          </div>
        </>
      ) : null}
      {agents.length > 0 ? (
        <>
          <SectionHeader label="Agents" count={agents.length} />
          <div className="px-1 pb-1">
            {agents.map((m) => (
              <MemberRow
                key={`${m.member_type}:${m.member_id}`}
                m={m}
                roleForMember={roleForMember}
                badgeForMember={badgeForMember}
                memberMenu={memberMenu}
                onGroupMemberAction={onGroupMemberAction}
                canRemove={canRemove}
                isMobile={isMobile}
                currentUserId={currentUserId}
                onOpenDm={onOpenDm}
                onOpenAgent={onOpenAgent}
                onOpenMember={onOpenMember}
                onRemove={onRemove}
                dmPending={dmPending}
                removeFailure={removeFailureFor?.(m)}
                roleFailure={roleFailureFor?.(m)}
                rolePendingAction={rolePendingActionFor?.(m) ?? null}
              />
            ))}
          </div>
        </>
      ) : null}
    </div>
  );
}
