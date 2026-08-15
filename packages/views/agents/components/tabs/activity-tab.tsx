"use client";

import { useEffect, useRef, useState } from "react";
import { AlertCircle, ArrowDown, Clock } from "lucide-react";
import { useRunnerActivity } from "@multica/core/agents";
import type { Agent } from "@multica/core/types";
import { Button } from "@multica/ui/components/ui/button";
import { useT } from "../../../i18n";
import {
  RunnerActivityTimeline,
  RunnerActivityTimelineLoading,
} from "../runner-activity-timeline";

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
    const bottom = anchorRef.current;
    if (!bottom || typeof IntersectionObserver === "undefined") return;
    const observer = new IntersectionObserver(
      ([entry]) => {
        if (entry) onReachedChangeRef.current(entry.isIntersecting);
      },
      { rootMargin: "0px 0px 40px 0px" },
    );
    observer.observe(bottom);
    return () => observer.disconnect();
  }, [anchorRef]);

  return <div ref={anchorRef} aria-hidden className="h-px w-full" />;
}

// ActivityTab keeps the previous timeline UI while consuming only the
// server-projected Runner presentation. It does not restore the retired legacy
// Activity event stream or infer provider/runtime semantics in the browser.
export function ActivityTab({ agent }: { agent: Agent }) {
  const { t } = useT("agents");
  const { data, isLoading, isError, refetch } = useRunnerActivity(agent.workspace_id, agent.id);
  const rootRef = useRef<HTMLDivElement>(null);
  const bottomRef = useRef<HTMLDivElement>(null);
  const atBottomRef = useRef(true);
  const landedRef = useRef(false);
  const [showJump, setShowJump] = useState(false);
  const timeline = [...(data?.timeline ?? [])].reverse();

  const previousAgentIdRef = useRef(agent.id);
  if (previousAgentIdRef.current !== agent.id) {
    previousAgentIdRef.current = agent.id;
    landedRef.current = false;
    atBottomRef.current = true;
    setShowJump(false);
  }

  useEffect(() => {
    if (timeline.length === 0) return;
    if (!landedRef.current || atBottomRef.current) {
      landedRef.current = true;
      bottomRef.current?.scrollIntoView({ block: "end" });
    }
  }, [timeline.length]);

  if (isLoading) {
    return <div className="p-6"><RunnerActivityTimelineLoading /></div>;
  }
  if (isError) {
    return (
      <div className="flex flex-col items-center justify-center gap-2 px-4 py-16 text-center" data-testid="activity-timeline-error">
        <AlertCircle className="size-8 text-destructive" aria-hidden />
        <p className="text-sm font-medium text-foreground">{t(($) => $.tab_body.activity.timeline_load_failed)}</p>
        <p className="max-w-xs text-xs text-muted-foreground">{t(($) => $.tab_body.activity.timeline_load_failed_hint)}</p>
        <Button type="button" variant="outline" size="sm" className="mt-2" onClick={() => void refetch()}>
          {t(($) => $.tab_body.activity.retry)}
        </Button>
      </div>
    );
  }

  return (
    <div ref={rootRef} className="p-6" data-testid="activity-tab">
      {timeline.length === 0 ? (
        <div className="flex flex-col items-center justify-center gap-2 px-4 py-16 text-center" data-testid="activity-timeline-empty">
          <Clock className="size-8 text-muted-foreground/50" aria-hidden />
          <p className="text-sm font-medium text-foreground">{t(($) => $.tab_body.activity.timeline_empty)}</p>
          <p className="max-w-xs text-xs text-muted-foreground">{t(($) => $.tab_body.activity.timeline_empty_hint)}</p>
        </div>
      ) : (
        <RunnerActivityTimeline rows={timeline} workspaceId={agent.workspace_id} />
      )}
      <StreamBottomAnchor
        anchorRef={bottomRef}
        onReachedChange={(reached) => {
          atBottomRef.current = reached;
          setShowJump(!reached);
        }}
      />
      {showJump && timeline.length > 0 ? (
        <div className="pointer-events-none sticky bottom-4 flex justify-center">
          <button
            type="button"
            className="pointer-events-auto inline-flex items-center gap-1.5 rounded-full border bg-background/95 px-3 py-1.5 text-xs font-medium text-foreground shadow-sm backdrop-blur transition-colors hover:bg-accent"
            onClick={() => bottomRef.current?.scrollIntoView({ behavior: "smooth", block: "end" })}
          >
            <ArrowDown className="size-3.5" aria-hidden />
            {t(($) => $.tab_body.activity.jump_to_latest)}
          </button>
        </div>
      ) : null}
    </div>
  );
}
