"use client";

import { useEffect, useRef, useState } from "react";
import { ArrowDown } from "lucide-react";
import type { Agent } from "@multica/core/types";
import { ActivityTimeline } from "./activity-timeline";
import { useAgentActivityEvents } from "./use-agent-activity-events";
import { ACTIVITY_CHROME_EN } from "./activity-event";

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
 */
export function ActivityTab({ agent }: ActivityTabProps) {
  const { events } = useAgentActivityEvents(agent.id);
  const bottomRef = useRef<HTMLDivElement>(null);
  // Whether the bottom sentinel is on screen = the reader is at the latest row
  // and live events should keep following. A ref (read inside the append effect
  // without re-subscribing) plus state (drives the pill).
  const atBottomRef = useRef(true);
  const [showJump, setShowJump] = useState(false);
  // Guards the one-time "land on newest" jump so it fires once per agent when the
  // first page arrives — not on every later append (those follow only when the
  // reader is already at the bottom).
  const landedRef = useRef(false);

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

  const handleReachedChange = (reached: boolean) => {
    atBottomRef.current = reached;
    setShowJump(!reached);
  };

  const jumpToLatest = () =>
    bottomRef.current?.scrollIntoView({ behavior: "smooth", block: "end" });

  return (
    <div className="p-6">
      <ActivityTimeline events={events} />
      <StreamBottomAnchor anchorRef={bottomRef} onReachedChange={handleReachedChange} />
      {showJump && (
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
