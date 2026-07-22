"use client";

import { useEffect, useState, type ReactNode } from "react";
import { cn } from "@multica/ui/lib/utils";
import {
  AGENT_XP_BURST_DURATION_MS,
  formatMemoryFileKeyLabel,
  useAgentXpBurstStore,
} from "@multica/core/agents/stores/xp-burst-store";

/**
 * Phase① memory XP feedback — gradient ring + floating「记忆 +N」on the agent
 * avatar. Subscribes to the shared burst store so every surface showing the
 * same agent animates in sync.
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
  const [activeKey, setActiveKey] = useState(0);
  const [visible, setVisible] = useState(false);

  useEffect(() => {
    if (!burst?.burstKey) return;
    setActiveKey(burst.burstKey);
    setVisible(true);
    const timer = window.setTimeout(() => setVisible(false), AGENT_XP_BURST_DURATION_MS);
    return () => window.clearTimeout(timer);
  }, [burst?.burstKey]);

  const label = burst ? formatMemoryFileKeyLabel(burst.fileKey) : "记忆";
  const delta = burst?.delta ?? 1;

  return (
    <span
      data-testid="agent-xp-burst"
      data-agent-id={agentId}
      data-burst-key={activeKey}
      className={cn("relative inline-flex shrink-0", className)}
    >
      {children}
      {visible && burst ? (
        <>
          <span
            aria-hidden
            data-testid="agent-xp-burst-ring"
            className="pointer-events-none absolute inset-0 rounded-full motion-safe:animate-[agent-xp-ring_1.2s_ease-out_forwards] motion-reduce:animate-none motion-reduce:opacity-0"
            style={{
              boxShadow: "0 0 0 2px color-mix(in srgb, var(--color-brand) 55%, transparent)",
            }}
          />
          <span
            data-testid="agent-xp-burst-chip"
            className="pointer-events-none absolute -right-1 -top-1 z-10 select-none whitespace-nowrap rounded-full bg-brand px-1.5 py-px text-[10px] font-semibold leading-tight text-brand-foreground shadow-sm motion-safe:animate-[agent-xp-chip_1.2s_ease-out_forwards] motion-reduce:animate-none"
          >
            {label}+{delta}
          </span>
        </>
      ) : null}
      <style>{`
        @keyframes agent-xp-ring {
          0% { opacity: 1; transform: scale(1); }
          100% { opacity: 0; transform: scale(1.22); }
        }
        @keyframes agent-xp-chip {
          0% { opacity: 0; transform: translateY(4px) scale(0.92); }
          12% { opacity: 1; transform: translateY(0) scale(1); }
          70% { opacity: 1; transform: translateY(-6px) scale(1); }
          100% { opacity: 0; transform: translateY(-12px) scale(0.96); }
        }
      `}</style>
    </span>
  );
}
