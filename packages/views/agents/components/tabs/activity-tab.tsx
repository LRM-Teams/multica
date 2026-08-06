"use client";

import { useState } from "react";
import { Copy } from "lucide-react";
import { useRunnerActivity } from "@multica/core/agents";
import type { Agent, RunnerActivityTimelineRow } from "@multica/core/types";
import { copyText } from "@multica/ui/lib/clipboard";
import { Button } from "@multica/ui/components/ui/button";
import { Skeleton } from "@multica/ui/components/ui/skeleton";

const TONE_DOT: Record<string, string> = {
  neutral: "bg-muted-foreground",
  active: "bg-brand",
  info: "bg-blue-500",
  warning: "bg-amber-500",
  error: "bg-destructive",
  success: "bg-emerald-500",
};

function formatLocalTime(value: string): string {
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? value : date.toLocaleString();
}

function TimelineRow({ row }: { row: RunnerActivityTimelineRow }) {
  const [expanded, setExpanded] = useState(false);
  const [copied, setCopied] = useState(false);
  const body = row.body?.trim();
  const canExpand = !!body && body.length > 180;
  const copy = async () => {
    if (!body || !(await copyText(body))) return;
    setCopied(true);
    window.setTimeout(() => setCopied(false), 2000);
  };
  return (
    <article className="flex gap-3 border-b py-3 last:border-b-0" data-testid="runner-activity-row">
      <span className={`mt-1.5 size-2 shrink-0 rounded-full ${TONE_DOT[row.tone] ?? TONE_DOT.neutral}`} />
      <div className="min-w-0 flex-1">
        <div className="flex items-baseline gap-3">
          <h3 className="min-w-0 truncate text-sm font-medium">{row.title}</h3>
          <time className="ml-auto shrink-0 text-xs text-muted-foreground" dateTime={row.occurred_at}>
            {formatLocalTime(row.occurred_at)}
          </time>
        </div>
        {row.subtext ? <p className="mt-0.5 truncate text-xs text-muted-foreground">{row.subtext}</p> : null}
        {body ? (
          <div className="mt-2 rounded-md bg-muted p-2 text-xs text-muted-foreground">
            <p className={expanded ? "whitespace-pre-wrap break-words" : "line-clamp-3 whitespace-pre-wrap break-words"}>{body}</p>
            <div className="mt-1 flex gap-2">
              {canExpand ? <button type="button" className="text-xs font-medium text-foreground" onClick={() => setExpanded((value) => !value)}>{expanded ? "Collapse" : "Expand"}</button> : null}
              <button type="button" className="inline-flex items-center gap-1 text-xs font-medium text-foreground" onClick={copy}><Copy className="size-3" />{copied ? "Copied" : "Copy"}</button>
            </div>
          </div>
        ) : null}
      </div>
    </article>
  );
}

// ActivityTab renders only server-projected Runner presentation. It has no
// task, presence, provider, session, or historical Activity-event fallback.
export function ActivityTab({ agent }: { agent: Agent }) {
  const { data, isLoading, isError, refetch } = useRunnerActivity(agent.workspace_id, agent.id);
  const timeline = data?.timeline ?? [];
  if (isLoading) return <div className="space-y-3 p-6"><Skeleton className="h-12" /><Skeleton className="h-12" /></div>;
  if (isError) return <div className="space-y-3 p-6 text-sm text-muted-foreground"><p>Could not load Activity.</p><Button size="sm" variant="outline" onClick={() => void refetch()}>Retry</Button></div>;
  return <section className="p-6" data-testid="activity-tab">
    {data?.summary?.visibility === "visible" ? <p className="mb-4 text-sm font-medium">{data.summary.label}</p> : null}
    {timeline.length === 0 ? <p className="text-sm text-muted-foreground">No activity yet.</p> : timeline.map((row) => <TimelineRow key={row.id} row={row} />)}
  </section>;
}
