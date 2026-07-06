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
import { resolveHealthDotClass } from "../agents/health";
import { AgentProfileCard } from "../agents/components/agent-profile-card";
import { AgentLivePeekCard } from "../agents/components/agent-live-peek-card";
import { MemberProfileCard } from "../members/member-profile-card";
import { SquadProfileCard } from "../squads/components/squad-profile-card";
import { availabilityConfig } from "../agents/presence";
import { useNavigation } from "../navigation";

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
  /**
   * Per-actor identity color forwarded to the base avatar's fallback
   * (monogram / bot icon). No effect when an avatar image renders. See
   * `agentColor`. Used by multi-agent surfaces (mention picker, group chat).
   */
  tint?: { fg: string; bg: string };
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
  enableHoverCard,
  showStatusDot,
  hoverCardVariant = "profile",
  profileLink,
  tint,
}: ActorAvatarProps) {
  const { getActorName, getActorInitials, getActorAvatarUrl } = useActorName();
  const paths = useWorkspacePaths();
  const avatar = (
    <ActorAvatarBase
      name={getActorName(actorType, actorId)}
      initials={getActorInitials(actorType, actorId)}
      avatarUrl={getActorAvatarUrl(actorType, actorId)}
      isAgent={actorType === "agent"}
      isSystem={actorType === "system"}
      isSquad={actorType === "squad"}
      size={size}
      className={className}
      tint={tint}
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
  const profileHref = shouldLinkToProfile
    ? actorType === "member"
      ? paths.memberDetail(actorId)
      : actorType === "agent"
        ? paths.agentDetail(actorId)
        : actorType === "squad"
          ? paths.squadDetail(actorId)
          : null
    : null;
  const content = profileHref ? (
    <ActorAvatarProfileLink href={profileHref}>{dotted}</ActorAvatarProfileLink>
  ) : (
    dotted
  );

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
  // Base avatar defaults to 20px when `size` is omitted — keep the box in sync
  // so it hugs the avatar exactly.
  const boxSize = size ?? 20;
  return (
    <span
      data-slot="agent-presence"
      className={cn("relative inline-flex shrink-0", className)}
      style={{ width: boxSize, height: boxSize }}
    >
      {children}
      <AgentStatusDot agentId={agentId} size={size} />
    </span>
  );
}

// Small presence indicator overlaid on the bottom-right of an agent avatar.
// Must live inside a fixed-size container (see `AgentPresenceOverlay`) so its
// absolute anchoring lands on the avatar, not a stretched grid cell. The dot
// diameter is proportional to the avatar size (≈28%, clamped to a legible
// minimum) so it reads correctly on both dense participant stacks (14–18px)
// and large avatars. Exported for surfaces that render the base avatar
// directly (e.g. comment trigger chips) but still want the standard dot.
export function AgentStatusDot({ agentId, size }: { agentId: string; size?: number }) {
  const ws = useCurrentWorkspace();
  const detail = useAgentPresenceDetail(ws?.id, agentId);
  // COLOR source: connectivity health (Iris §1) — the SAME source as the
  // Activity tab Health block, so the dot can never drift from the tab.
  const { summary: healthSummary } = useAgentHealth(agentId);
  if (detail === "loading") return null;

  const { dotClass: availabilityDotClass, label } =
    availabilityConfig[detail.availability];
  // TODO(#266): once the BE health API is live, health_summary.state is the
  // SOLE dot color source. Until then the dot degrades to the availability
  // color when the summary is missing (endpoint not deployed / loading) so it
  // never blanks or crashes. STRUCTURE (fixed box, proportional dot, cut-out
  // ring) and PULSE (a workload overlay, below) are unchanged — orthogonal to
  // color.
  const dotClass = resolveHealthDotClass(healthSummary, availabilityDotClass);
  // Diameter tracks the avatar so the indicator is proportional everywhere,
  // with a floor so it never disappears on the smallest (14–16px) avatars.
  const diameter = Math.max(5, Math.round((size ?? 24) * 0.28));
  const dotStyle = { width: diameter, height: diameter };
  // "Working" is a motion cue layered on top of the online color, not a new
  // color — amber is already taken by `unstable`. A slow breathing pulse
  // communicates "online + actively running a task" without colliding with
  // the availability palette. `idle` / `queued` / non-online stay static.
  //
  // Gate the pulse on the SAME connectivity axis as the dot color (Iris): a
  // disconnected agent must never appear to be "working". When health is known,
  // pulse only on a healthy link (online / recovered); when the summary is
  // absent (transitional / no record yet) fall back to the availability signal,
  // consistent with the color fallback above.
  const connectivityOk = healthSummary
    ? healthSummary.state === "online" || healthSummary.state === "recovered"
    : detail.availability === "online";
  const isWorking = connectivityOk && detail.workload === "working";
  const statusLabel = isWorking ? `${label} · Working` : label;

  return (
    <span className="absolute bottom-0 right-0 inline-flex">
      {isWorking && (
        // Motion layer only — hidden under prefers-reduced-motion so the
        // static dot below remains the sole (accessible) status signal.
        // aria-hidden: the label on the static dot already conveys "Working".
        <span
          aria-hidden="true"
          style={dotStyle}
          className={`absolute inline-flex animate-ping rounded-full ${dotClass} opacity-60 motion-reduce:hidden`}
        />
      )}
      {/* `ring-background` is a cut-out ring the color of the surface behind the
          dot, so it stays legible on dark/light/hover/selected backgrounds. */}
      <span
        aria-label={`Status: ${statusLabel}`}
        title={statusLabel}
        style={dotStyle}
        className={`relative rounded-full ring-2 ring-background ${dotClass} ${
          isWorking ? "motion-reduce:ring-brand" : ""
        }`}
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
