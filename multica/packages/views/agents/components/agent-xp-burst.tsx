"use client";

import { useEffect, useRef, useState, type ReactNode } from "react";
import { cn } from "@multica/ui/lib/utils";
import {
  type AgentXpBurstSnapshot,
  useAgentXpBurstStore,
} from "@multica/core/agents/stores";

/** Ring + float label duration (design-memory-feedback-phase1-v2). */
export const AGENT_XP_BURST_ANIMATION_MS = 1100;

/**
 * Phase① memory XP feedback — neutral outline ring + weak gray「+N」on the
 * agent avatar. Subscribes to the shared burst store so every opted-in
 * surface for the same agent animates in sync.
 */
export function AgentXpBurst({
  agentId,
  children,
  className,
}: {
  agentId: string;
  children: ReactNode;
  className?: string;
}) {
  const burst = useAgentXpBurstStore((s) => s.bursts[agentId]);
  const [displayBurst, setDisplayBurst] = useState<AgentXpBurstSnapshot | null>(null);
  const lastBurstKeyRef = useRef(0);

  if (burst && burst.burstKey !== lastBurstKeyRef.current) {
    lastBurstKeyRef.current = burst.burstKey;
    setDisplayBurst(burst);
  }

  useEffect(() => {
    if (!displayBurst) return;
    const timer = window.setTimeout(
      () => setDisplayBurst(null),
      AGENT_XP_BURST_ANIMATION_MS,
    );
    return () => window.clearTimeout(timer);
  }, [displayBurst]);

  const visible = displayBurst !== null;
  const delta = displayBurst?.delta ?? 1;

  return (
    <span
      data-testid="agent-xp-burst"
      data-agent-id={agentId}
      data-burst-key={displayBurst?.burstKey ?? 0}
      className={cn("relative inline-flex shrink-0", className)}
    >
      {children}
      {visible ? (
        <>
          <span
            aria-hidden
            data-testid="agent-xp-burst-ring"
            className="pointer-events-none absolute inset-0 rounded-full border border-muted-foreground/35 motion-safe:animate-[agent-xp-ring_1.1s_ease-out_forwards] motion-reduce:animate-none motion-reduce:opacity-0"
          />
          <span
            data-testid="agent-xp-burst-chip"
            className="pointer-events-none absolute -right-0.5 -top-1 z-10 select-none text-[10px] font-medium tabular-nums leading-none text-muted-foreground motion-safe:animate-[agent-xp-chip_1.1s_ease-out_forwards] motion-reduce:animate-none"
          >
            +{delta}
          </span>
        </>
      ) : null}
      <style>{`
        @keyframes agent-xp-ring {
          0% { opacity: 0.85; transform: scale(1); }
          100% { opacity: 0; transform: scale(1.18); }
        }
        @keyframes agent-xp-chip {
          0% { opacity: 0; transform: translateY(3px); }
          15% { opacity: 1; transform: translateY(0); }
          65% { opacity: 0.9; transform: translateY(-5px); }
          100% { opacity: 0; transform: translateY(-10px); }
        }
      `}</style>
    </span>
  );
}
