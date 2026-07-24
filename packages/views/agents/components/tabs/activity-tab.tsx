"use client";

import { useEffect, useLayoutEffect, useRef, useState, type ReactNode } from "react";
import { ArrowDown } from "lucide-react";
import type { Agent } from "@multica/core/types";
import { resolveActorDisplayName } from "@multica/core/identity";
import { cn } from "@multica/ui/lib/utils";
import { ActorAvatar } from "../../../common/actor-avatar";
import { ActivityTimeline } from "./activity-timeline";
import { useAgentActivityEvents } from "./use-agent-activity-events";
import {
  ACTIVITY_CHROME_EN,
  ACTIVITY_LABEL_EN,
  ACTIVITY_TONE_DOT_CLASS,
  activityPresentation,
  formatActivityRelativeTime,
  type ActivityEvent,
} from "./activity-event";

interface ActivityTabProps {
  agent: Agent;
}

/**
 * Zero-height anchor at the end of the stream. Reports (via `onReachedChange`)
 * whether it is on screen — i.e. whether the reader is at the latest row — using
 * an IntersectionObserver against the panel/page scroll wrapper. Kept as its own
 * component (like `InfiniteScrollSentinel`) so the observer subscription lives in
 * a mount effect that only calls a ref'd callback, never `setState` — the follow
 * state it drives stays out of a mount effect on the parent.
 */
function StreamBottomAnchor({
  anchorRef,
  onReachedChange,
}: {
  anchorRef: React.RefObject<HTMLDivElement | null>;
  onReachedChange: (reached: boolean) => void;
}) {
  const onReachedChangeRef = useRef(onReachedChange);
  onReachedChangeRef.current = onReachedChange;

  useEffect(() => {
    const node = anchorRef.current;
    if (!node) return;
    // A small bottom rootMargin: any sliver of the anchor on screen counts as
    // "at the latest".
    const observer = new IntersectionObserver(
      (entries) => {
        const entry = entries[0];
        if (entry) onReachedChangeRef.current(entry.isIntersecting);
      },
      { rootMargin: "0px 0px 40px 0px" },
    );
    observer.observe(node);
    return () => observer.disconnect();
  }, [anchorRef]);

  return <div ref={anchorRef} aria-hidden className="h-px w-full" />;
}

/**
 * Zero-height anchor at the START of the stream — the mirror of
 * {@link StreamBottomAnchor}. Reports (via `onReachedChange`) whether it is on
 * screen so the tab can fetch the next (older) page as the reader scrolls up to
 * read history. A generous top `rootMargin` prefetches that older page slightly
 * before the reader hits the very top, so the prepend lands without a stall.
 */
function StreamTopAnchor({
  anchorRef,
  onReachedChange,
}: {
  anchorRef: React.RefObject<HTMLDivElement | null>;
  onReachedChange: (reached: boolean) => void;
}) {
  const onReachedChangeRef = useRef(onReachedChange);
  onReachedChangeRef.current = onReachedChange;

  useEffect(() => {
    const node = anchorRef.current;
    if (!node) return;
    const observer = new IntersectionObserver(
      (entries) => {
        const entry = entries[0];
        if (entry) onReachedChangeRef.current(entry.isIntersecting);
      },
      { rootMargin: "200px 0px 0px 0px" },
    );
    observer.observe(node);
    return () => observer.disconnect();
  }, [anchorRef]);

  return <div ref={anchorRef} aria-hidden className="h-px w-full" />;
}

/**
 * The nearest scrollable ancestor of `node` — the panel/page wrapper the tab is
 * mounted in (the tab intentionally does not own a scroll container). Used to
 * re-anchor the reader's viewport when an older page prepends above it. Returns
 * null when none is found (e.g. jsdom), so callers no-op safely.
 */
function getScrollParent(node: HTMLElement | null): HTMLElement | null {
  let el: HTMLElement | null = node?.parentElement ?? null;
  while (el) {
    const overflowY = getComputedStyle(el).overflowY;
    if (
      (overflowY === "auto" || overflowY === "scroll" || overflowY === "overlay") &&
      el.scrollHeight > el.clientHeight
    ) {
      return el;
    }
    el = el.parentElement;
  }
  return null;
}

/**
 * LRM-563 page header: agent face + name + latest-status line (tone dot +
 * action word + relative time from `projectLatestActivity`).
 */
function ActivityTabHeader({
  agent,
  latest,
  isLoading,
}: {
  agent: Agent;
  latest: ActivityEvent | null;
  isLoading: boolean;
}) {
  const displayName = resolveActorDisplayName(agent, agent.id);

  let status: ReactNode = null;
  if (isLoading && !latest) {
    status = (
      <span className="inline-flex min-w-0 items-center gap-1.5 text-[12px] text-muted-foreground">
        <span className="size-1.5 shrink-0 rounded-full bg-muted-foreground/40" aria-hidden />
        <span className="truncate">{ACTIVITY_CHROME_EN.loading}</span>
      </span>
    );
  } else if (latest) {
    const presentation = activityPresentation(latest);
    const label = ACTIVITY_LABEL_EN[presentation.labelKey];
    status = (
      <span
        className="inline-flex min-w-0 items-center gap-1.5 text-[12px] text-muted-foreground"
        data-testid="activity-tab-latest-status"
      >
        <span
          className={cn(
            "size-1.5 shrink-0 rounded-full",
            ACTIVITY_TONE_DOT_CLASS[presentation.tone],
          )}
          aria-hidden
        />
        <span className="truncate">
          {label}
          <span className="text-muted-foreground/70">
            {" · "}
            {formatActivityRelativeTime(latest.occurred_at)}
          </span>
        </span>
      </span>
    );
  }

  return (
    <header
      className="mb-5 flex items-center gap-3"
      data-testid="activity-tab-header"
    >
      <ActorAvatar
        actorType="agent"
        actorId={agent.id}
        size={36}
        name={displayName}
        avatarUrlHint={agent.avatar_url}
        profileLink={false}
        showStatusDot
      />
      <div className="min-w-0 flex-1">
        <h2 className="truncate text-[15px] font-semibold leading-tight text-foreground">
          {displayName}
        </h2>
        {status}
      </div>
    </header>
  );
}

/**
 * Agent Activity tab (#351) — a single, raft-aligned, time-ordered event
 * stream: `time · status dot · human label · optional detail`, newest work
 * flowing down the column. It replaces the old Now / Last-30-days / Recent-work
 * aggregate cards, which contradicted each other ("29 runs" above "nothing
 * finished yet") and mixed "is it reliable" with "what did it deliver".
 *
 * The render is the shared #267 `ActivityTimeline` (which also powers the
 * profile/hover card in `compact` mode — one surface, one source). All rows
 * come from the #302 raw `ActivityEvent` facts; FE projects display label/tone
 * from stable kind/reason fields and never renders raw command output. The
 * default user surface does not expose diagnostics controls.
 *
 * Scroll (#421): the stream is chronological (oldest → newest, newest at the
 * bottom), so opening the tab lands on the newest row and live events append to
 * the bottom and auto-follow — UNLESS the reader has scrolled up to read
 * history, in which case following pauses and a "jump to latest" pill appears.
 * The scroll container is the panel/page wrapper (`overflow-y-auto`) both mount
 * sites already provide; we drive it via a bottom sentinel + IntersectionObserver
 * so the same code works in the side panel and the overview pane without owning a
 * scroll container of our own (and without touching the row render).
 *
 * History (#620): the REST stream is cursor-paginated, so scrolling UP to the
 * top loads the next (older) page instead of the first ~50 events being a hard
 * ceiling (a high-frequency agent's newer rows had buried old `Running command`
 * rows past the first page, so they looked "gone"). A top sentinel mirrors the
 * bottom one to trigger the fetch, and the added height is compensated against
 * the scroll container so the reader's viewport stays anchored (no jump).
 *
 * Full-page chrome (LRM-563 / LRM-558 P2): agent header + four states
 * (loading skeleton without spine / empty / error+retry / populated spine).
 */
export function ActivityTab({ agent }: ActivityTabProps) {
  const {
    events,
    latest,
    isLoading,
    isError,
    refetch,
    loadOlder,
    hasOlder,
    isLoadingOlder,
  } = useAgentActivityEvents(agent.id);
  const rootRef = useRef<HTMLDivElement>(null);
  const topRef = useRef<HTMLDivElement>(null);
  const bottomRef = useRef<HTMLDivElement>(null);
  // Scroll metrics captured when an older-page load starts, so the reader's
  // viewport can be re-anchored after the older rows prepend above it (otherwise
  // the added height shoves their content down — a visible upward jump).
  const olderLoadAnchorRef = useRef<{
    scrollTop: number;
    scrollHeight: number;
  } | null>(null);
  const wasLoadingOlderRef = useRef(false);
  // Whether the bottom sentinel is on screen = the reader is at the latest row
  // and live events should keep following. A ref (read inside the append effect
  // without re-subscribing) plus state (drives the pill).
  const atBottomRef = useRef(true);
  const [showJump, setShowJump] = useState(false);
  // Guards the one-time "land on newest" jump so it fires once per agent when the
  // first page arrives — not on every later append (those follow only when the
  // reader is already at the bottom).
  const landedRef = useRef(false);

  // Show loading skeleton only on first paint (no rows yet). Error only when
  // the query failed and we have nothing to show — never confuse with empty.
  const showLoading = isLoading && events.length === 0;
  const showError = isError && events.length === 0 && !isLoading;

  // Re-arm the follow state when the agent changes (the tab component is reused).
  // Done inline during render via the prev-prop pattern rather than an effect, so
  // there's no extra commit showing the previous agent's state
  // (react.dev/learn/you-might-not-need-an-effect#adjusting-some-state-when-a-prop-changes).
  const prevAgentIdRef = useRef(agent.id);
  if (prevAgentIdRef.current !== agent.id) {
    prevAgentIdRef.current = agent.id;
    landedRef.current = false;
    atBottomRef.current = true;
    setShowJump(false);
  }

  // Land on the newest row when the first page arrives; afterwards append-follow
  // only while the reader is already at the bottom (so scrolling up to read
  // history is never yanked back down). `agent.id` is a dependency (alongside the
  // prev-prop `landedRef` reset) so switching to another agent always re-lands —
  // even when the new agent happens to have the SAME event count (cached, no
  // empty→fill transition), where `events.length` alone would not re-run this.
  useEffect(() => {
    if (events.length === 0) return;
    if (!landedRef.current) {
      landedRef.current = true;
      bottomRef.current?.scrollIntoView({ block: "end" });
    } else if (atBottomRef.current) {
      bottomRef.current?.scrollIntoView({ block: "end" });
    }
  }, [events.length, agent.id]);

  // Re-anchor the reader's viewport after an older page has prepended. Runs on
  // the commit where `isLoadingOlder` flips back to false (react-query lands the
  // new pages and the flag together), so it never fires for a bottom WS append.
  useLayoutEffect(() => {
    if (wasLoadingOlderRef.current && !isLoadingOlder) {
      const anchor = olderLoadAnchorRef.current;
      const el = getScrollParent(rootRef.current);
      if (anchor && el) {
        el.scrollTop = anchor.scrollTop + (el.scrollHeight - anchor.scrollHeight);
      }
      olderLoadAnchorRef.current = null;
    }
    wasLoadingOlderRef.current = isLoadingOlder;
  }, [isLoadingOlder]);

  // The reader scrolled up to the top of the loaded history → fetch the next
  // (older) page, capturing the pre-prepend scroll metrics for re-anchoring.
  const handleTopReached = (reached: boolean) => {
    if (!reached || !hasOlder || isLoadingOlder) return;
    const el = getScrollParent(rootRef.current);
    olderLoadAnchorRef.current = el
      ? { scrollTop: el.scrollTop, scrollHeight: el.scrollHeight }
      : null;
    loadOlder();
  };

  const handleReachedChange = (reached: boolean) => {
    atBottomRef.current = reached;
    setShowJump(!reached);
  };

  const jumpToLatest = () =>
    bottomRef.current?.scrollIntoView({ behavior: "smooth", block: "end" });

  return (
    <div ref={rootRef} className="p-6">
      <ActivityTabHeader agent={agent} latest={latest} isLoading={showLoading} />
      <StreamTopAnchor anchorRef={topRef} onReachedChange={handleTopReached} />
      <ActivityTimeline
        events={events}
        isLoading={showLoading}
        isError={showError}
        onRetry={refetch}
      />
      <StreamBottomAnchor anchorRef={bottomRef} onReachedChange={handleReachedChange} />
      {showJump && !showLoading && !showError && events.length > 0 && (
        <div className="pointer-events-none sticky bottom-4 flex justify-center">
          <button
            type="button"
            onClick={jumpToLatest}
            className="pointer-events-auto inline-flex items-center gap-1.5 rounded-full border bg-background/95 px-3 py-1.5 text-xs font-medium text-foreground shadow-sm backdrop-blur transition-colors hover:bg-accent"
          >
            <ArrowDown className="size-3.5" />
            {ACTIVITY_CHROME_EN.jump_to_latest}
          </button>
        </div>
      )}
    </div>
  );
}
