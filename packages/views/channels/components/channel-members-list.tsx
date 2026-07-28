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
import { ActorAvatar } from "../../common/actor-avatar";
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
  const isAgent = m.member_type === "agent";
  const presentation: ActorIdentityPresentation = resolveActorIdentityPresentation(
    m,
    isAgent ? t(($) => $.message.agent_badge) : t(($) => $.members.title),
  );
  const roleKey = roleForMember(m);
  const showMutedRole = !isAgent && (roleKey === "owner" || roleKey === "admin");
  // The group member panel passes `badgeForMember` → show the channel/group role
  // (owner / 群管 / 管理员; ordinary members get no badge). Everywhere else, keep
  // the existing workspace-role label.
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

  // #832 临时诚实守卫：整块删除（勿翻开关/勿部分启用）。删除条件 = **#814 的角色写
  // 接口 merged + served**，且本菜单三行已接真实授权 mutation（届时由实现真 UI 的
  // 那张任务一并删除）。
  //
  // 条件写任务号、不写 PR 号：PR 会滚（#1321 → #1326 → #1332 …），指向一个已经合并
  // 的前身会让人以为条件已满足、提前把守卫拆掉。任务号不会滚。
  //
  // Why it exists: the role mutations (promote / demote / transfer) have no
  // write endpoint yet, so these rows used to be clickable and answer with an
  // info toast. Frank set a group manager, saw the toast, and believed it had
  // worked — the click itself promises the action is available, and a toast
  // that disappears is no substitute for saying so up front.
  //
  // There is deliberately NO feature flag here. `disabled` is unconditional.
  // A flag would create a legal-looking half-open state — rows clickable with
  // no handler behind them, i.e. the original bug minus even the toast. This
  // block has exactly one valid exit: delete it and replace it with rows wired
  // to the real authorized mutation. One exit ⇒ no switch. (Parker)
  const rolePendingNoteId = `group-member-role-pending-${m.member_type}-${m.member_id}`;
  const showRolePendingNote =
    !!menuActions &&
    (menuActions.canPromoteToManager ||
      menuActions.canDemoteToMember ||
      menuActions.canTransferOwnership);

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
          profileLink={false}
        />
        <div className="min-w-0 flex-1">
          <div className="flex min-w-0 items-baseline gap-1.5">
            <span className="truncate text-sm font-semibold text-ink">
              {presentation.displayName}
            </span>
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
                disabled
                aria-describedby={rolePendingNoteId}
                data-testid="group-member-menu-promote"
              >
                {isAgent
                  ? t(($) => $.members.menu.promote_agent)
                  : t(($) => $.members.menu.promote_human)}
              </DropdownMenuItem>
            )}
            {menuActions.canDemoteToMember && (
              <DropdownMenuItem
                disabled
                aria-describedby={rolePendingNoteId}
                data-testid="group-member-menu-demote"
              >
                {isAgent
                  ? t(($) => $.members.menu.demote_agent)
                  : t(($) => $.members.menu.demote_human)}
              </DropdownMenuItem>
            )}
            {menuActions.canTransferOwnership && (
              <DropdownMenuItem
                disabled
                aria-describedby={rolePendingNoteId}
                data-testid="group-member-menu-transfer"
              >
                {t(($) => $.members.menu.transfer)}
              </DropdownMenuItem>
            )}
            {showRolePendingNote && (
              // #832 — persistent, always-visible explanation. NOT a tooltip and
              // NOT a post-click toast: the user must see "this can't be done
              // yet" BEFORE reaching for it. Rendered as a real text node (and
              // referenced by each disabled row's aria-describedby) so assistive
              // tech announces the reason together with the row, rather than
              // just "dimmed".
              <p
                id={rolePendingNoteId}
                data-testid="group-member-menu-role-pending"
                className="px-2 py-1.5 text-xs leading-relaxed text-muted-foreground"
              >
                {t(($) => $.members.menu.role_actions_pending)}
              </p>
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
  className,
}: {
  /**
   * #839 — returns the in-row failure notice for a member, or undefined. Given
   * per member (not a single "last error") so a second failure cannot silently
   * replace an unresolved first one.
   */
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
              />
            ))}
          </div>
        </>
      ) : null}
    </div>
  );
}
