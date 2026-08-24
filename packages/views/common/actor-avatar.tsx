"use client";

import { useEffect, useRef, useState } from "react";
import { cn } from "@multica/ui/lib/utils";
import { ActorAvatarBase } from "@multica/ui/components/common/actor-avatar";
import {
  HoverCard,
  HoverCardTrigger,
  HoverCardContent,
} from "@multica/ui/components/ui/hover-card";
import { Tooltip, TooltipTrigger, TooltipContent } from "@multica/ui/components/ui/tooltip";
import { useActorName } from "@multica/core/workspace/hooks";
import { isDirectoryActorMiss } from "@multica/core/workspace/resolved-actor-name";
import { useMemberOnline } from "@multica/core/workspace/use-member-presence";
import { useAgentPresence, type AgentPresence } from "@multica/core/agents";
import { useCurrentWorkspace, useWorkspacePaths } from "@multica/core/paths";
import { useAgentPanelStore } from "@multica/core/agents/stores";
import { useMemberPanelStore } from "@multica/core/workspace";
import { ActorProfileContent } from "./actor-profile-popover";
import {
  availabilityConfig,
  toLiveAvailability,
  type LiveAvailability,
} from "../agents/presence";
import { useNavigation } from "../navigation";
import { useOpenAgentPanel } from "./agent-panel-context";
import { useOpenMemberPanel } from "./member-panel-context";
import { resolveIdentityAvatarUrl } from "./identity-avatar-cache";
import { AgentXpBurst } from "../agents/components/agent-xp-burst";
import { FleetRankPennantOverlay } from "@multica/ui/components/fleet/fleet-class-badge";
import {
  mentionTypeFromActorType,
  useResolvedActorIdentity,
} from "./use-resolved-actor-identity";

interface ActorAvatarProps {
  actorType: string;
  actorId: string;
  size?: number;
  className?: string;
  /**
   * Optional display name from a row/message payload. Prefer this over the
   * workspace directory when the actor is hidden from list endpoints (e.g.
   * group-manager Beckham) so the glyph fallback still shows the real name.
   */
  name?: string;
  /**
   * Optional face URL from a row/message payload (LRM-224). Only accelerates
   * the identity cache — null / undefined must not clear a known face.
   */
  avatarUrlHint?: string | null;
  /**
   * Wrap the avatar in a hover-card preview on dwell. Use for "who is this?"
   * surfaces — comment authors, list rows, subscriber chips. Independent of
   * `showStatusDot`: a surface can have one, both, or neither. Payload is
   * always `ActorProfileContent` (task #25 — one sitewide identity hover).
   */
  enableHoverCard?: boolean;
  /**
   * Overlay a presence status dot at the avatar's bottom-right. Agents use
   * server-owned Agent Presence; members use WS online (LRM-462). Independent of
   * `enableHoverCard` so picker rows can show the dot without nesting a
   * popover inside the dropdown.
   */
  showStatusDot?: boolean;
  /** Optional page-level Presence snapshot avoids one Query observer per row. */
  agentPresence?: AgentPresence | "loading";
  /**
   * Make the avatar click through to the actor page. Defaults on for members
   * and agents, while picker/menu controls keep their own click behavior.
   */
  profileLink?: boolean;
  /**
   * Phase① memory XP burst on this avatar (message rows / profile only — not
   * sidebar lists). No effect for non-agent actors.
   */
  showXpBurst?: boolean;
  /** Top-3 fleet rank pennant on agent avatars. */
  fleetRank?: number;
}


const FOCUSABLE_ANCESTOR_SELECTOR =
  'a[href], button:not([disabled]), [role="button"]:not([aria-disabled="true"]), [tabindex]:not([tabindex="-1"])';
const PROFILE_LINK_CONTROL_SELECTOR =
  'button, [role^="menuitem"], [role="option"], [data-slot="dropdown-menu-item"], [data-slot="dropdown-menu-checkbox-item"], [data-slot="popover-trigger"]';
/**
 * LRM-809: an interactive ancestor may explicitly OPT IN to keeping the
 * avatar's profile entry alive by carrying this attribute (e.g. the Activity
 * feed row, where avatar click = profile and row click = open item). Without
 * it, picker/menu rows keep the historical "outer control wins" behavior.
 */
const AVATAR_PROFILE_ENTRY_ATTR = "data-avatar-profile-entry";
/** Control ancestors carrying the opt-in do not swallow avatar clicks. */
function isProfileEntryAllowed(controlAncestor: Element | null | undefined): boolean {
  return !!controlAncestor?.hasAttribute?.(AVATAR_PROFILE_ENTRY_ATTR);
}

export function ActorAvatar({
  actorType,
  actorId,
  size,
  className,
  name: nameOverride,
  avatarUrlHint,
  enableHoverCard,
  showStatusDot,
  agentPresence,
  profileLink,
  showXpBurst = false,
  fleetRank,
}: ActorAvatarProps) {
  const { getActorName, getActorInitials, getActorAvatarUrl } = useActorName();
  // LRM-391: ListAgents hides channel/private / group-manager agents — resolve
  // name+face via member-profile so read-only chrome never shows "Unknown Agent".
  const mentionType = mentionTypeFromActorType(actorType);
  const profileIdentity = useResolvedActorIdentity(actorId, mentionType);
  const directoryName = getActorName(actorType, actorId);
  const liveName =
    nameOverride?.trim() ||
    profileIdentity.displayName ||
    (isDirectoryActorMiss(directoryName) ? actorId : directoryName);
  // LRM-224: identity-first — directory + sticky cache; message URL only seeds.
  const avatarUrl = resolveIdentityAvatarUrl({
    actorType,
    actorId,
    avatarUrlHint,
    directoryUrl:
      profileIdentity.avatarUrl ?? getActorAvatarUrl(actorType, actorId),
  });
  const displayName = liveName;
  const initials = /[a-z]/i.test(displayName.charAt(0))
    ? displayName.charAt(0).toUpperCase()
    : displayName.charAt(0) || getActorInitials(actorType, actorId);
  const avatar = (
    <ActorAvatarBase
      name={displayName}
      initials={initials}
      avatarUrl={avatarUrl}
      isAgent={actorType === "agent"}
      isSystem={actorType === "system"}
      isSquad={actorType === "squad"}
      size={size}
      toneSeed={`${actorType}:${actorId}`}
      className={className}
    />
  );

  // Optional presence dot overlay. Agents use server Presence; members use
  // WS online (LRM-462). Wrapping unconditionally would create extra DOM for
  // every avatar; we only wrap when a dot is asked for.
  const wrapDot =
    !!showStatusDot && (actorType === "agent" || actorType === "member");
  const dotted = !wrapDot
    ? avatar
    : actorType === "agent" ? (
        <AgentPresenceOverlay agentId={actorId} size={size} presence={agentPresence}>
          {avatar}
        </AgentPresenceOverlay>
      ) : (
        <MemberPresenceOverlay userId={actorId} size={size}>
          {avatar}
        </MemberPresenceOverlay>
      );
  const withFleetPennant =
    actorType === "agent" && fleetRank ? (
      <span className="relative inline-flex">
        {dotted}
        <FleetRankPennantOverlay fleetRank={fleetRank} />
      </span>
    ) : (
      dotted
    );
  const withXpBurst =
    actorType === "agent" && showXpBurst ? (
      <AgentXpBurst agentId={actorId}>{withFleetPennant}</AgentXpBurst>
    ) : (
      withFleetPennant
    );
  const shouldLinkToProfile =
    profileLink ??
    (actorType === "member" || actorType === "agent");
  // Agents open the #349 side panel; humans open the LRM-619 member Profile
  // dock (inline via MemberPanelProvider, else global member panel store).
  const content = !shouldLinkToProfile
    ? withXpBurst
    : actorType === "agent"
      ? <ActorAvatarPanelTrigger agentId={actorId}>{withXpBurst}</ActorAvatarPanelTrigger>
      : actorType === "member"
        ? (
            <ActorAvatarMemberPanelTrigger userId={actorId}>
              {withXpBurst}
            </ActorAvatarMemberPanelTrigger>
          )
        : withXpBurst;

  if (!enableHoverCard) {
    return content;
  }
  if (actorType === "agent") {
    return (
      <AgentAvatarHoverCard agentId={actorId}>{content}</AgentAvatarHoverCard>
    );
  }
  if (actorType === "member") {
    return <MemberAvatarHoverCard userId={actorId}>{content}</MemberAvatarHoverCard>;
  }
  return content;
}

/**
 * Opens the #349 agent side panel on click. Prefers the local
 * `AgentPanelProvider` (channels/DM — panel renders inline, replacing the
 * thread-panel slot, per Frank's direction) and falls back to the global
 * `useAgentPanelStore` (every other surface — panel renders as an overlay at
 * the dashboard-layout level) when no local provider is in scope. The two
 * mechanisms never both fire for the same click since the global store is
 * only reachable when the local context is absent.
 *
 * Same nested-clickable guard as member/agent panel triggers: a picker row or
 * menu item that owns its own click keeps that behavior instead of opening
 * the panel underneath it.
 */
function ActorAvatarPanelTrigger({
  agentId,
  children,
}: {
  agentId: string;
  children: React.ReactNode;
}) {
  const paths = useWorkspacePaths();
  const { openInNewTab } = useNavigation();
  const openFromContext = useOpenAgentPanel();
  const openFromStore = useAgentPanelStore((s) => s.open);
  const selectedMemberId = useMemberPanelStore((s) => s.selectedUserId);
  const closeMember = useMemberPanelStore((s) => s.close);
  const open = openFromContext ?? openFromStore;

  const handleOpen = (event: React.MouseEvent | React.KeyboardEvent) => {
    const controlAncestor = event.currentTarget.parentElement?.closest(
      PROFILE_LINK_CONTROL_SELECTOR,
    );
    if (controlAncestor && !isProfileEntryAllowed(controlAncestor)) return;

    event.preventDefault();
    event.stopPropagation();
    if (
      "metaKey" in event &&
      (event.metaKey || event.ctrlKey || event.shiftKey) &&
      openInNewTab
    ) {
      openInNewTab(paths.agentDetail(agentId));
      return;
    }
    // LRM-877 — if a human Profile dock is open (global), push Agent with
    // returnTo so `← {name}` can pop back. Local AgentPanelProvider hosts
    // inherit returnTo from their own member sidePanel state.
    const returnToMemberId = selectedMemberId ?? undefined;
    closeMember();
    open(
      agentId,
      undefined,
      returnToMemberId ? { returnToMemberId } : undefined,
    );
  };

  return (
    // Deliberately a span-with-role, not a real <button>: an avatar frequently
    // renders inside a row/card that is itself a link or button, and a real
    // <button> here would be invalid interactive nesting. Same pattern the
    // sibling `ActorAvatarProfileLink` uses (`role="link"` on a span). The
    // control-ancestor guard above defers to the outer interactive when present.
    // react-doctor-disable-next-line react-doctor/prefer-tag-over-role -- span+role avoids invalid nested-interactive DOM (see comment)
    <span
      role="button"
      tabIndex={-1}
      // overflow-visible: presence ring/dot must not be shaved by rounded clip
      // from this hit target (LRM-1119).
      className="inline-flex cursor-pointer overflow-visible rounded-full"
      onClick={handleOpen}
      onKeyDown={(event) => {
        if (event.key === "Enter" || event.key === " ") {
          handleOpen(event);
        }
      }}
    >
      {children}
    </span>
  );
}

/** LRM-619 — open human member Profile dock (local provider or global store). */
function ActorAvatarMemberPanelTrigger({
  userId,
  children,
}: {
  userId: string;
  children: React.ReactNode;
}) {
  const paths = useWorkspacePaths();
  const { openInNewTab } = useNavigation();
  const openFromContext = useOpenMemberPanel();
  const openFromStore = useMemberPanelStore((s) => s.open);
  const closeAgent = useAgentPanelStore((s) => s.close);
  const open = openFromContext ?? openFromStore;

  const handleOpen = (event: React.MouseEvent | React.KeyboardEvent) => {
    const controlAncestor = event.currentTarget.parentElement?.closest(
      PROFILE_LINK_CONTROL_SELECTOR,
    );
    if (controlAncestor && !isProfileEntryAllowed(controlAncestor)) return;

    event.preventDefault();
    event.stopPropagation();
    if (
      "metaKey" in event &&
      (event.metaKey || event.ctrlKey || event.shiftKey) &&
      openInNewTab
    ) {
      openInNewTab(paths.actorProfile("user", userId));
      return;
    }
    closeAgent();
    open(userId);
  };

  return (
    // react-doctor-disable-next-line react-doctor/prefer-tag-over-role -- span+role avoids invalid nested-interactive DOM
    <span
      role="button"
      tabIndex={-1}
      className="inline-flex cursor-pointer overflow-visible rounded-full"
      onClick={handleOpen}
      onKeyDown={(event) => {
        if (event.key === "Enter" || event.key === " ") {
          handleOpen(event);
        }
      }}
    >
      {children}
    </span>
  );
}

/**
 * The single, stretch-proof presence container. Renders a **fixed-size,
 * non-stretchable** box exactly the avatar's size, with the status dot
 * absolutely anchored to its bottom-right.
 *
 * Root cause this guards against: the old wrapper was a bare
 * `relative inline-flex` whose height was `auto`, so when its parent was a CSS
 * grid / flex row with the default `align-items: stretch` (e.g. the channel
 * message bubble's `grid grid-cols-[28px_…]`), the wrapper stretched to the
 * full row height. The dot's `absolute bottom-0 right-0` then anchored to the
 * stretched box's bottom — i.e. the message's bottom-left — instead of the
 * avatar. Pinning an **explicit** width/height (not `self-start`) makes the
 * box immune to stretch — `align-items: stretch` only stretches items whose
 * cross size is `auto` — while preserving centering in `items-center` rows, so
 * every existing face keeps its alignment and the dot can never detach.
 *
 * This is the ONE implementation of avatar + presence; all faces route through
 * it (via `ActorAvatar showStatusDot` or, for surfaces holding a base avatar,
 * directly) so there are no hand-rolled, stretch-fragile wrappers.
 */
export function AgentPresenceOverlay({
  agentId,
  size,
  className,
  presence,
  children,
}: {
  agentId: string;
  size?: number;
  className?: string;
  presence?: AgentPresence | "loading";
  children: React.ReactNode;
}) {
  // Base avatar defaults to 20px when `size` is omitted — keep the box in sync
  // so it hugs the avatar exactly.
  const boxSize = size ?? 20;
  return (
    <span
      data-slot="agent-presence"
      // overflow-visible: keep the corner badge from being rectangular-clipped
      // by overflow:hidden ancestors (DM sidebar scroller / rounded hit
      // targets). LRM-1119.
      className={cn("relative inline-flex shrink-0 overflow-visible", className)}
      style={{ width: boxSize, height: boxSize }}
    >
      {children}
      <AgentStatusDot agentId={agentId} size={size} presence={presence} />
    </span>
  );
}

// Presence badge overlaid on the bottom-right of an agent avatar. Must live
// inside a fixed-size container (see `AgentPresenceOverlay`) so its absolute
// anchoring lands on the avatar, not a stretched grid cell. Exported for
// surfaces that render the base avatar directly (e.g. comment trigger chips).
export function AgentStatusDot({
  agentId,
  size,
  presence,
}: {
  agentId: string;
  size?: number;
  presence?: AgentPresence | "loading";
}) {
  if (presence !== undefined) {
    return <AgentStatusDotVisual presence={presence} size={size} />;
  }
  return <QueriedAgentStatusDot agentId={agentId} size={size} />;
}

function QueriedAgentStatusDot({ agentId, size }: { agentId: string; size?: number }) {
  const ws = useCurrentWorkspace();
  const presence = useAgentPresence(ws?.id, agentId);
  return <AgentStatusDotVisual presence={presence} size={size} />;
}

function AgentStatusDotVisual({
  presence,
  size,
}: {
  presence: AgentPresence | "loading";
  size?: number;
}) {
  if (presence === "loading") return null;
  const live = toLiveAvailability(presence);
  if (!live) return null;
  return <PresenceBadge live={live} size={size} />;
}

function presenceBadgeMetrics(size = 20): { badge: number; core: number } {
  if (size <= 20) return { badge: 6, core: 4 };
  if (size <= 32) return { badge: 8, core: 6 };
  if (size <= 48) return { badge: 10, core: 8 };
  return { badge: 12, core: 10 };
}

/**
 * Two-layer presence mark shared by agents and members. The surface-colored
 * badge separates status from the avatar without a heavy halo; the smaller
 * core alone carries meaning. Discrete tiers keep large profile avatars from
 * producing oversized indicators while preserving dense-stack legibility.
 */
function PresenceBadge({ live, size }: { live: LiveAvailability; size?: number }) {
  const { label } = availabilityConfig[live];
  const { badge, core } = presenceBadgeMetrics(size);
  const coreClass =
    live === "online"
      ? "bg-success"
      : "border border-muted-foreground/50 bg-transparent";

  return (
    <span className="pointer-events-none absolute bottom-0 right-0 z-[1] inline-flex">
      <Tooltip>
        <TooltipTrigger
          render={
            <span
              aria-label={`Status: ${label}`}
              style={{ width: badge, height: badge }}
              className="relative inline-flex items-center justify-center rounded-full bg-background"
            >
              <span
                data-slot="presence-core"
                style={{ width: core, height: core }}
                className={`rounded-full ${coreClass}`}
              />
            </span>
          }
        />
        <TooltipContent side="top">{label}</TooltipContent>
      </Tooltip>
    </span>
  );
}

/** Stretch-proof wrapper for human-member presence dots (LRM-462). */
export function MemberPresenceOverlay({
  userId,
  size,
  className,
  children,
}: {
  userId: string;
  size?: number;
  className?: string;
  children: React.ReactNode;
}) {
  const boxSize = size ?? 20;
  return (
    <span
      data-slot="member-presence"
      className={cn("relative inline-flex shrink-0 overflow-visible", className)}
      style={{ width: boxSize, height: boxSize }}
    >
      {children}
      <MemberStatusDot userId={userId} size={size} />
    </span>
  );
}

/** Common-IM online/offline dot for human members (WS session presence). */
export function MemberStatusDot({ userId, size }: { userId: string; size?: number }) {
  const ws = useCurrentWorkspace();
  const online = useMemberOnline(ws?.id, userId);
  if (online === "loading") return null;

  const live = online ? "online" : "offline";
  return <PresenceBadge live={live} size={size} />;
}

/**
 * Wraps an agent avatar in a hover-card. The trigger is keyboard-focusable
 * only when no focusable ancestor (link/button) already provides a tab stop —
 * this prevents nested tabbable descendants and keyboard-nav bloat at sites
 * where the avatar lives inside a row link or click target.
 *
 * Same `ActorProfileContent` as group/DM `ActorProfileTrigger` — task #25:
 * one hover card sitewide (human + agent).
 */
function AgentAvatarHoverCard({
  agentId,
  children,
}: {
  agentId: string;
  children: React.ReactNode;
}) {
  return (
    <ActorAvatarHoverCardShell
      content={<ActorProfileContent memberType="agent" memberId={agentId} />}
    >
      {children}
    </ActorAvatarHoverCardShell>
  );
}

function MemberAvatarHoverCard({
  userId,
  children,
}: {
  userId: string;
  children: React.ReactNode;
}) {
  return (
    <ActorAvatarHoverCardShell
      content={<ActorProfileContent memberType="user" memberId={userId} />}
    >
      {children}
    </ActorAvatarHoverCardShell>
  );
}

// Common chrome shared between agent and member hover cards. Keeps focus
// behaviour and width consistent with `ActorProfileTrigger` (task #25).
function ActorAvatarHoverCardShell({
  content,
  children,
}: {
  content: React.ReactNode;
  children: React.ReactNode;
}) {
  const triggerRef = useRef<HTMLSpanElement>(null);
  const [standalone, setStandalone] = useState(false);

  useEffect(() => {
    const el = triggerRef.current;
    if (!el) return;
    const ancestor = el.parentElement?.closest(FOCUSABLE_ANCESTOR_SELECTOR);
    setStandalone(!ancestor);
  }, []);

  return (
    <HoverCard>
      <HoverCardTrigger
        render={<span ref={triggerRef} />}
        tabIndex={standalone ? 0 : -1}
        className={
          standalone
            ? "inline-flex cursor-pointer rounded-full focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
            : "inline-flex cursor-pointer"
        }
      >
        {children}
      </HoverCardTrigger>
      <HoverCardContent align="start" className="w-[300px] p-0">
        {content}
      </HoverCardContent>
    </HoverCard>
  );
}
