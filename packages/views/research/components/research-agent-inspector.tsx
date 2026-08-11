"use client";

import type { ExecutionRow } from "../execution-overlay";
import { Button } from "@multica/ui/components/ui/button";
import { cn } from "@multica/ui/lib/utils";
import { useT } from "../../i18n/use-t";

export function ResearchAgentInspector({
  row,
  open,
  onClose,
  onOpenAgentConfig,
  className,
}: {
  row: ExecutionRow | null;
  open: boolean;
  onClose: () => void;
  onOpenAgentConfig?: () => void;
  className?: string;
}) {
  const { t } = useT("research");

  if (!open || !row) return null;

  return (
    <aside
      data-testid="research-agent-inspector"
      className={cn("research-agent-inspector open", className)}
    >
      <header className="agent-head">
        <button type="button" className="agent-close" onClick={onClose} aria-label={t(($) => $.d5.inspector.close)}>
          ×
        </button>
        <div className="who">
          <div className="agent-big-avatar">{row.initials || row.name.slice(0, 2).toUpperCase()}</div>
          <div>
            <b>{row.name}</b>
            <span>{row.action || row.actionDetail || row.status}</span>
          </div>
        </div>
      </header>
      <div className="agent-body">
        <div className="agent-objective">
          <small>{t(($) => $.d5.inspector.objective)}</small>
          <b>{row.taskObjective || row.action || t(($) => $.d5.inspector.no_task)}</b>
        </div>
        {row.stage ? (
          <p className="mt-3 text-[11px] text-muted-foreground">
            {t(($) => $.d5.inspector.phase, { phase: row.stage })}
          </p>
        ) : null}
        {row.recentResult ? (
          <section className="work-block">
            <h4>{t(($) => $.d5.inspector.completed)}</h4>
            <div className="work-item done">{row.recentResult.title}</div>
          </section>
        ) : null}
        {row.action ? (
          <section className="work-block">
            <h4>{t(($) => $.d5.inspector.current)}</h4>
            <div className="work-item live">{row.action}</div>
          </section>
        ) : null}
      </div>
      <footer className="agent-foot">
        {onOpenAgentConfig ? (
          <Button type="button" size="sm" variant="outline" onClick={onOpenAgentConfig}>
            {t(($) => $.d5.inspector.open_agent)}
          </Button>
        ) : null}
      </footer>
    </aside>
  );
}
