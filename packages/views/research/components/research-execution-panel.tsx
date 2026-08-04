"use client";

import { useId, useState } from "react";
import { Activity, Check, ChevronDown, CircleDashed, Clock3, LocateFixed, Pause, TriangleAlert, X, type LucideIcon } from "lucide-react";
import { Button } from "@multica/ui/components/ui/button";
import { cn } from "@multica/ui/lib/utils";
import type { ResearchExecutionAgent, ResearchExecutionStatus } from "../lib/research-execution-panel-fixture";

type StatusPresentation = { label: string; Icon: LucideIcon; badgeClass: string; markerClass: string };
const LOCATABLE_PREFIX = "可定位至";
const LOCATE_PREFIX = "定位至";
const STATUS_PRESENTATION: Record<ResearchExecutionStatus, StatusPresentation> = {
  queued: { label: "排队", Icon: Clock3, badgeClass: "bg-muted text-muted-foreground", markerClass: "border-dashed border-muted-foreground/55 text-muted-foreground" },
  running: { label: "执行中", Icon: Activity, badgeClass: "bg-brand/10 text-brand", markerClass: "border-brand/35 bg-brand/10 text-brand" },
  done: { label: "完成", Icon: Check, badgeClass: "bg-success/10 text-success-strong", markerClass: "border-success/30 bg-success/10 text-success-strong" },
  failed: { label: "失败", Icon: X, badgeClass: "bg-destructive/10 text-destructive-strong", markerClass: "border-destructive/30 bg-destructive/10 text-destructive-strong" },
  stale: { label: "停滞", Icon: TriangleAlert, badgeClass: "bg-warning/10 text-warning", markerClass: "border-warning/35 bg-warning/10 text-warning" },
  idle: { label: "空闲", Icon: Pause, badgeClass: "bg-muted text-muted-foreground", markerClass: "border-muted-foreground/35 text-muted-foreground" },
};

function AgentAvatar({ agent }: { agent: ResearchExecutionAgent }) {
  return agent.avatarUrl ? <img src={agent.avatarUrl} alt="" className="size-8 shrink-0 rounded-lg object-cover" /> : <span aria-hidden="true" className="flex size-8 shrink-0 items-center justify-center rounded-lg bg-muted text-[11px] font-semibold text-foreground">{agent.initials}</span>;
}

function ExecutionRow({ agent, onLocate }: { agent: ResearchExecutionAgent; onLocate?: (agent: ResearchExecutionAgent) => void }) {
  const [expanded, setExpanded] = useState(false);
  const detailId = useId();
  const presentation = STATUS_PRESENTATION[agent.status];
  const StatusIcon = presentation.Icon;
  const hasDetail = Boolean(agent.actionDetail || agent.failureReason || agent.locationLabel);
  return (
    <article data-testid="research-execution-row" data-status={agent.status} className="relative min-w-0 overflow-hidden border-b border-border/60 px-3 py-2.5 last:border-b-0">
      {agent.status === "running" ? <span aria-hidden="true" className="absolute inset-x-0 top-0 h-px overflow-hidden bg-brand/15"><span className="block h-full w-1/2 bg-brand animate-nav-progress-sweep motion-reduce:hidden" /></span> : null}
      <div className="grid min-w-0 grid-cols-[auto_minmax(0,1fr)_auto] items-start gap-x-2">
        <AgentAvatar agent={agent} />
        <div className="min-w-0">
          <div className="flex min-w-0 items-baseline gap-1.5"><h3 className="truncate text-xs font-semibold text-foreground">{agent.name}</h3><span className="truncate text-[11px] text-muted-foreground">{agent.role}</span></div>
          <p className="mt-0.5 truncate text-xs leading-5 text-foreground" title={agent.action}>{agent.action}</p>
          <div className="mt-0.5 flex min-w-0 items-center gap-1.5 text-[11px] text-muted-foreground"><span className="truncate">{agent.timeLabel}</span>{agent.locationLabel ? <><span aria-hidden="true">·</span><span className="truncate">{LOCATABLE_PREFIX} {agent.locationLabel}</span></> : null}</div>
        </div>
        <div className="flex items-center gap-1">
          <span className={cn("inline-flex h-6 items-center gap-1 rounded-md px-1.5 text-[10px] font-semibold", presentation.badgeClass)}><span className={cn("inline-flex size-4 items-center justify-center rounded-full border", presentation.markerClass)}><StatusIcon className="size-2.5" strokeWidth={2.4} /></span>{presentation.label}</span>
          {hasDetail ? <Button type="button" variant="ghost" size="icon" className="size-7 shrink-0 rounded-md focus-visible:ring-2" aria-label={`${expanded ? "收起" : "展开"}${agent.name}的执行详情`} aria-expanded={expanded} aria-controls={detailId} onClick={() => setExpanded((value) => !value)}><ChevronDown aria-hidden="true" className={cn("size-3.5 transition-transform motion-reduce:transition-none", expanded && "rotate-180")} /></Button> : null}
        </div>
      </div>
      {expanded ? <div id={detailId} className="ml-10 mt-2 min-w-0 rounded-lg border border-border/70 bg-muted/35 p-2.5">
        {agent.actionDetail ? <p className="break-words text-xs leading-5 text-foreground">{agent.actionDetail}</p> : null}
        {agent.failureReason ? <p className="mt-1.5 flex gap-1.5 text-xs leading-5 text-destructive-strong"><TriangleAlert className="mt-1 size-3 shrink-0" aria-hidden="true" /><span className="break-words">{agent.failureReason}</span></p> : null}
        {agent.locationLabel && onLocate ? <Button type="button" variant="outline" size="sm" className="mt-2 min-h-9 max-w-full gap-1.5 px-2 text-xs" onClick={() => onLocate(agent)}><LocateFixed className="size-3.5 shrink-0" aria-hidden="true" /><span className="truncate">{LOCATE_PREFIX} {agent.locationLabel}</span></Button> : null}
      </div> : null}
    </article>
  );
}

export function ResearchExecutionPanel({ agents, title = "执行动态", className, onLocate }: { agents: readonly ResearchExecutionAgent[]; title?: string; className?: string; onLocate?: (agent: ResearchExecutionAgent) => void }) {
  const runningCount = agents.filter((agent) => agent.status === "running").length;
  return <section aria-label={title} className={cn("min-w-0 overflow-hidden rounded-xl border border-border bg-card shadow-sm", className)}>
    <header className="flex min-h-10 min-w-0 items-center justify-between gap-3 border-b border-border/70 px-3 py-2"><div className="flex min-w-0 items-center gap-2"><CircleDashed className="size-4 shrink-0 text-muted-foreground" aria-hidden="true" /><h2 className="truncate text-xs font-semibold text-foreground">{title}</h2></div><p className="shrink-0 text-[11px] tabular-nums text-muted-foreground">{runningCount > 0 ? `${runningCount} 个智能体执行中` : "暂无智能体执行"}</p></header>
    <div className="min-w-0">{agents.map((agent) => <ExecutionRow key={agent.id} agent={agent} onLocate={onLocate} />)}</div>
  </section>;
}
