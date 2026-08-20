"use client";

import { useEffect, useRef, useState } from "react";
import { UnicodeSpinner } from "@multica/ui/components/common/unicode-spinner";
import type { AgentPresence } from "@multica/core/agents";
import type { ChatPendingTask } from "@multica/core/types";
import { formatElapsedSecs } from "../lib/format";
import { useT } from "../../i18n";

interface Props {
  /** Server-authoritative pending-task snapshot (`created_at` anchors the timer). */
  pendingTask: ChatPendingTask;
  /** Resolved presence; pass `undefined` to suppress availability hints. */
  availability: AgentPresence | undefined;
}

interface Stage {
  label: string;
  static?: boolean;
}

type StageKey = "offline" | "thinking";

/**
 * Standalone chat (FAB / notes bubble) no longer streams live task:message
 * stages. Pending is either "agent offline" or "Thinking" until chat:done.
 */
export function pickStageKeys(
  status: string | undefined,
  availability: AgentPresence | undefined,
): { stageKey: StageKey; static?: boolean } {
  if (
    availability === "offline" &&
    (status === "queued" || status === "dispatched" || status === "running" || !status)
  ) {
    return { stageKey: "offline", static: true };
  }
  return { stageKey: "thinking" };
}

function useResolveStage(): (
  status: string | undefined,
  availability: AgentPresence | undefined,
) => Stage {
  const { t } = useT("chat");
  return (status, availability) => {
    const decision = pickStageKeys(status, availability);
    return {
      label: t(($) => $.status_pill.stages[decision.stageKey]),
      static: decision.static,
    };
  };
}

export function TaskStatusPill({
  pendingTask,
  availability,
}: Props) {
  const resolveStage = useResolveStage();
  // Anchor: locked on first render. Once set we never reassign — otherwise
  // the timer would visibly snap backwards when an optimistic-seeded
  // `Date.now()` anchor is later replaced by a server-side created_at that
  // happened a few hundred ms earlier. Monotonic elapsed > strict accuracy.
  const anchorRef = useRef<number | null>(null);
  if (anchorRef.current === null) {
    if (pendingTask.created_at) {
      const t = Date.parse(pendingTask.created_at);
      anchorRef.current = Number.isFinite(t) ? t : Date.now();
    } else {
      anchorRef.current = Date.now();
    }
  }
  const anchor = anchorRef.current;

  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    const timer = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(timer);
  }, []);

  const elapsedSecs = Math.max(0, Math.floor((now - anchor) / 1000));
  const stage = resolveStage(pendingTask.status, availability);

  return (
    <div
      className="flex items-center gap-1.5 px-1 text-xs text-muted-foreground"
      aria-live="polite"
    >
      {!stage.static && (
        <UnicodeSpinner name="breathe" className="shrink-0 opacity-70" />
      )}
      {/* No text shimmer: the horizontal wash makes short CJK labels look
          like they wobble left/right. Motion stays on the spinner only. */}
      <span className="truncate">
        {stage.label}
        <span className="opacity-70"> · {formatElapsedSecs(elapsedSecs)}</span>
      </span>
    </div>
  );
}
