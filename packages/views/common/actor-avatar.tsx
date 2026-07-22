"use client";

import { useEffect, useRef, useState } from "react";
import { cn } from "@multica/ui/lib/utils";
import { ActorAvatar as ActorAvatarBase } from "@multica/ui/components/common/actor-avatar";
import {
  HoverCard,
  HoverCardTrigger,
  HoverCardContent,
} from "@multica/ui/components/ui/hover-card";
import { useActorName } from "@multica/core/workspace/hooks";
import {
  useAgentHealth,
  useAgentPresenceDetail,
} from "@multica/core/agents";
import { useCurrentWorkspace, useWorkspacePaths } from "@multica/core/paths";
import { useAgentPanelStore } from "@multica/core/agents/stores";
import { resolveLiveHealthDotClass } from "../agents/health";
import { AgentProfileCard } from "../agents/components/agent-profile-card";
import { AgentLivePeekCard } from "../agents/components/agent-live-peek-card";
import { MemberProfileCard } from "../members/member-profile-card";
import { SquadProfileCard } from "../squads/components/squad-profile-card";
import { availabilityConfig, toLivePresence } from "../agents/presence";
import { useNavigation } from "../navigation";
import { useOpenAgentPanel } from "./agent-panel-context";
import { resolveIdentityAvatarUrl } from "./identity-avatar-cache";

/**
 * Selects which agent hover-card payload to render when `enableHoverCard` is
 * on. Two surfaces, two intents:
 * - `"profile"` (default) — static identity (description, runtime, skills,
 *   owner). Used by 20+ "who is this agent?" surfaces (comment authors,
 *   pickers, list rows).
 * - `"live"` — live activity peek (workload, current issue, last activity).
 *   Used where the user already knows the identity and wants the live state,
 *   e.g. the squad members tab.
 *
 * Has no effect for non-agent actors (members always render the member card).
 */
export type AgentHoverCardVariant = "profile" | "live";

interface ActorAvatarProps {
  actorType: string;
  actorId: string;
  size?: number;
  className?: string;
  /**
   * Optional face URL from a row/message payload (LRM-224). Only accelerates
   * the identity cache — null / undefined must not clear a known face.
   */
  avatarUrlHint?: string | null;
  /**
   * Wrap the avatar in a hover-card preview on dwell. Use for "who is this?"
   * surfaces — comment authors, list rows, subscriber chips. Independent of
   * `showStatusDot`: a surface can have one, both, or neither.
   */
  enableHoverCard?: boolean;
  /**
   * Overlay an agent-presence dot at the avatar's bottom-right. Use at
   * decision moments (picker rows, current-assignee display, agent-centric
   * surfaces). Has no effect for non-agent actors. Independent of
   * `enableHoverCard` so picker rows can show the dot without nesting a
   * popover inside the dropdown.
   */
  showStatusDot?: boolean;
  /**
   * When `enableHoverCard` is on for an agent, choose which payload to
   * render. See {@link AgentHoverCardVariant}. Defaults to `"profile"` so
   * existing call sites keep their identity-card behaviour.
   */
  hoverCardVariant?: AgentHoverCardVariant;
  /**
   * Make the avatar click through to the actor page. Defaults on for members
   * and agents, while picker/menu controls keep their own click behavior.
   */
  profileLink?: boolean;
}


/** Isolated so message bubbles / agent dots never call useWorkspacePaths. */
function ActorAvatarWorkspaceProfileLink({
  actorType,
  actorId,
  children,
}: {
  actorType: "member" | "squad" | string;
  actorId: string;
  children: React.ReactNode;
}) {
  const workspacePaths = useWorkspacePaths();
  const href =
    actorType === "member"
      ? workspacePaths.memberDetail(actorId)
      : actorType === "squad"
        ? workspacePaths.squadDetail(actorId)
        : null;
  return href ? (
    <ActorAvatarProfileLink href={href}>{children}</ActorAvatarProfileLink>
  ) : (
    <>{children}</>
  );
}

const FOCUSABLE_ANCESTOR_SELECTOR =
  'a[href], button:not([disabled]), [role="button"]:not([aria-disabled="true"]), [tabindex]:not([tabindex="-1"])';
const PROFILE_LINK_CONTROL_SELECTOR =
  'button, [role^="menuitem"], [role="option"], [data-slot="dropdown-menu-item"], [data-slot="dropdown-menu-checkbox-item"], [data-slot="popover-trigger"]';

export function ActorAvatar({
  actorType,
  actorId,
  size,
  className,
  avatarUrlHint,
  enableHoverCard,
  showStatusDot,
  hoverCardVariant = "profile",
  profileLink,
}: ActorAvatarProps) {
  const { getActorName, getActorInitials, getActorAvatarUrl } = useActorName();
  // LRM-224: identity-first — directory + sticky cache; message URL only seeds.
  const avatarUrl = resolveIdentityAvatarUrl({
    actorType,
    actorId,
    avatarUrlHint,
    directoryUrl: getActorAvatarUrl(actorType, actorId),
  });
  const avatar = (
    <ActorAvatarBase
      name={getActorName(actorType, actorId)}
      initials={getActorInitials(actorType, actorId)}
      avatarUrl={avatarUrl}
      isAgent={actorType === "agent"}
      isSystem={actorType === "system"}
      isSquad={actorType === "squad"}
      size={size}
      toneSeed={`${actorType}:${actorId}`}
      className={className}
    />
  );

  // Optional presence dot overlay. Only meaningful for agents — members have
  // no presence backbone. Wrapping unconditionally would create extra DOM for
  // every avatar; we only wrap when a dot is asked for. `AgentPresenceOverlay`
  // is the single, stretch-proof presence container (see its doc comment).
  const wrapDot = showStatusDot && actorType === "agent";
  const dotted = wrapDot ? (
    <AgentPresenceOverlay agentId={actorId} size={size}>
      {avatar}
    </AgentPresenceOverlay>
  ) : (
    avatar
  );
  const shouldLinkToProfile =
    profileLink ??
    (actorType === "member" || actorType === "agent" || actorType === "squad");
  // Agents open the #349 side panel (inline in channels/DM via
  // AgentPanelProvider, a global overlay everywhere else via the fallback
  // store — see agent-panel-context.tsx / panel-store.ts) instead of routing
  // to the full agent detail page. Members/squads still route — no side
  // panel exists for those actor types yet.
  const content = !shouldLinkToProfile
    ? dotted
    : actorType === "agent"
      ? <ActorAvatarPanelTrigger agentId={actorId}>{dotted}</ActorAvatarPanelTrigger>
      : actorType === "member" || actorType === "squad"
        ? (
            <ActorAvatarWorkspaceProfileLink actorType={actorType} actorId={actorId}>
              {dotted}
            </ActorAvatarWorkspaceProfileLink>
          )
        : dotted;

  if (!enableHoverCard) {
    return content;
  }
  if (actorType === "agent") {
    return (
      <AgentAvatarHoverCard agentId={actorId} variant={hoverCardVariant}>
        {content}
      </AgentAvatarHoverCard>
    );
  }
  if (actorType === "member") {
    return <MemberAvatarHoverCard userId={actorId}>{content}</MemberAvatarHoverCard>;
  }
  if (actorType === "squad") {
    return <SquadAvatarHoverCard squadId={actorId}>{content}</SquadAvatarHoverCard>;
  }
  return content;
}

function ActorAvatarProfileLink({
  href,
  children,
}: {
  href: string;
  children: React.ReactNode;
}) {
  const { push, openInNewTab } = useNavigation();

  const navigate = (event: React.MouseEvent | React.KeyboardEvent) => {
    const controlAncestor = event.currentTarget.parentElement?.closest(
      PROFILE_LINK_CONTROL_SELECTOR,
    );
    if (controlAncestor) return;

    event.preventDefault();
    event.stopPropagation();
    if (
      "metaKey" in event &&
      (event.metaKey || event.ctrlKey || event.shiftKey) &&
      openInNewTab
    ) {
      openInNewTab(href);
      return;
    }
    push(href);
  };

  return (
    <span
      role="link"
      tabIndex={-1}
      className="inline-flex cursor-pointer rounded-full"
      onClick={navigate}
      onKeyDown={(event) => {
        if (event.key === "Enter" || event.key === " ") {
          navigate(event);
        }
      }}
    >
      {children}
    </span>
  );
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
 * Same nested-clickable guard as `ActorAvatarProfileLink`: a picker row or
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
  const open = openFromContext ?? openFromStore;

  const handleOpen = (event: React.MouseEvent | React.KeyboardEvent) => {
    const controlAncestor = event.currentTarget.parentElement?.closest(
      PROFILE_LINK_CONTROL_SELECTOR,
    );
    if (controlAncestor) return;

    event.preventDefault();
    event.stopPropagation();
    // ⌘/ctrl/shift-click keeps the power-user path to the full detail page
    // (new tab) instead of the peek panel — same escape hatch
    // ActorAvatarProfileLink already gave every other actor type.
    if (
      "metaKey" in event &&
      (event.metaKey || event.ctrlKey || event.shiftKey) &&
      openInNewTab
    ) {
      openInNewTab(paths.agentDetail(agentId));
      return;
    }
    open(agentId);
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
      className="inline-flex cursor-pointer rounded-full"
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
  children,
}: {
  agentId: string;
  size?: number;
  className?: string;
  children: React.ReactNode;
}) {
  const boxSize = size ?? 20;
  const ws = useCurrentWorkspace();
  const detail = useAgentPresenceDetail(ws?.id, agentId);
  const archived =
    detail !== "loading" &&
    detail != null &&
    toLivePresence(detail.availability) === "archived";

  return (
    <span
      data-slot="agent-presence"
      data-archived={archived ? "true" : undefined}
      className={cn(
        "relative inline-flex shrink-0",
        // LRM-248: archived / deleted — grayscale avatar, no live corner dot.
        archived && "grayscale opacity-70",
        className,
      )}
      style={{ width: boxSize, height: boxSize }}
    >
      {children}
      {!archived ? <AgentStatusDot agentId={agentId} size={size} /> : null}
    </span>
  );
}

// LRM-248: only Online (green) / Offline (gray). No Working pulse, no Unstable.
export function AgentStatusDot({ agentId, size }: { agentId: string; size?: number }) {
  const ws = useCurrentWorkspace();
  const detail = useAgentPresenceDetail(ws?.id, agentId);
  const { summary: healthSummary } = useAgentHealth(agentId);
  if (detail === "loading") return null;

  const live = toLivePresence(detail.availability);
  if (live === "archived") return null;

  const availabilityDotClass =
    live === "online"
      ? availabilityConfig.online.dotClass
      : availabilityConfig.offline.dotClass;
  const dotClass = resolveLiveHealthDotClass(healthSummary, availabilityDotClass);
  const diameter = Math.max(5, Math.round((size ?? 24) * 0.28));
  const dotStyle = { width: diameter, height: diameter };
  const statusLabel = live === "online" ? "Online" : "Offline";

  const HOLLOW_MIN_PX = 8;
  const isOfflineHollow = live === "offline" && diameter >= HOLLOW_MIN_PX;
  const dotColorClass = isOfflineHollow
    ? "border-2 border-muted-foreground/50 bg-transparent"
    : dotClass;

  return (
    <span className="absolute bottom-0 right-0 inline-flex">
      <span
        aria-label={`Status: ${statusLabel}`}
        title={statusLabel}
        style={dotStyle}
        className={`relative rounded-full ring-2 ring-background ${dotColorClass}`}
      />
    </span>
  );
}

/**
 * Wraps an agent avatar in a hover-card. The trigger is keyboard-focusable
 * only when no focusable ancestor (link/button) already provides a tab stop —
 * this prevents nested tabbable descendants and keyboard-nav bloat at sites
 * where the avatar lives inside a row link or click target.
 */
function AgentAvatarHoverCard({
  agentId,
  variant,
  children,
}: {
  agentId: string;
  variant: AgentHoverCardVariant;
  children: React.ReactNode;
}) {
  const content =
    variant === "live" ? (
      <AgentLivePeekCard agentId={agentId} />
    ) : (
      <AgentProfileCard agentId={agentId} />
    );
  return (
    <ActorAvatarHoverCardShell content={content}>
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
    <ActorAvatarHoverCardShell content={<MemberProfileCard userId={userId} />}>
      {children}
    </ActorAvatarHoverCardShell>
  );
}

function SquadAvatarHoverCard({
  squadId,
  children,
}: {
  squadId: string;
  children: React.ReactNode;
}) {
  return (
    <ActorAvatarHoverCardShell content={<SquadProfileCard squadId={squadId} />}>
      {children}
    </ActorAvatarHoverCardShell>
  );
}

// Common chrome shared between agent and member hover cards. Keeps focus
// behaviour and width consistent so the two surfaces feel structurally
// parallel — content varies, frame doesn't.
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
      <HoverCardContent align="start" className="w-72">
        {content}
      </HoverCardContent>
    </HoverCard>
  );
}
