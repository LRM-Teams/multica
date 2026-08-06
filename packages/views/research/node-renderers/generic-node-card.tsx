"use client";

/**
 * Research V6 — GenericNodeCard (UI-01 / LRM-1475).
 *
 * Safe degradation card for any node kind the V6 registry does not recognise.
 * Unlike the older `v6-common` generic, this card uses ONLY semantic tokens
 * (no hardcoded hex) and renders every bounded field that is still safe to
 * show (title / summary / status / actor / evidence), plus the recorded
 * diagnostic. It never throws — the page never crashes on unknown kinds.
 *
 * Renders a native `<button>` when `onOpen` is provided (keyboard/screen-reader
 * safe), a plain `<article>` otherwise — satisfies react-doctor's
 * non-interactive-element-interactions rule.
 */

import type { ResearchV6UnknownKindDiagnostic } from "@multica/core/types/research-v6";
import { HelpCircle } from "lucide-react";
import { cn } from "@multica/ui/lib/utils";
import { stateVisualFor } from "./node-state-matrix";

export interface GenericNodeCardProps {
  nodeId: string;
  kind: string;
  title: string;
  summary?: string;
  status?: string;
  diagnostic?: ResearchV6UnknownKindDiagnostic;
  /** Zoom density tier (mirror of NodeCardShell). */
  zoom?: 0.4 | 1 | 1.6;
  onOpen?: () => void;
}

export function GenericNodeCard({
  nodeId,
  kind,
  title,
  summary,
  status,
  diagnostic,
  zoom = 1,
  onOpen,
}: GenericNodeCardProps) {
  const compact = zoom <= 0.4;
  const interactive = Boolean(onOpen);
  const state = stateVisualFor("unknown");

  const base = cn(
    "group/node relative w-52 overflow-hidden rounded-lg border border-border bg-muted/50 text-left",
    interactive && "cursor-pointer hover:shadow-md",
  );

  const cardInner = (
    <>
      <div data-testid="generic-accent-bar" className={cn("h-1 w-full", state.accentBarClass)} />
      <div className="space-y-1 p-2.5">
        <header className="flex items-center gap-1.5">
          <HelpCircle
            data-testid="generic-icon"
            className="h-3.5 w-3.5 shrink-0 text-muted-foreground"
          />
          <span
            data-testid="generic-type-badge"
            className="truncate text-[10px] font-medium uppercase tracking-wide text-muted-foreground"
          >
            未知类型
          </span>
          <span
            data-testid="generic-raw-kind"
            className="ml-auto truncate rounded bg-muted-foreground/10 px-1 text-[9px] font-mono text-muted-foreground"
          >
            {kind}
          </span>
        </header>
        <h3 data-testid="generic-title" className="line-clamp-2 text-sm font-medium leading-snug">
          {title || nodeId}
        </h3>
        {summary && !compact && (
          <p data-testid="generic-summary" className="line-clamp-2 text-xs text-muted-foreground">
            {summary}
          </p>
        )}
        <div className="flex items-center gap-1 pt-0.5">
          <span
            data-testid="generic-state"
            className="text-[10px] font-medium uppercase tracking-wide text-muted-foreground"
          >
            {status || state.label}
          </span>
        </div>
        {!compact && diagnostic && (
          <details data-testid="generic-diagnostics" className="pt-0.5 text-[9px] text-muted-foreground">
            <summary className="cursor-pointer">无法识别的类型</summary>
            <dl className="mt-0.5 font-mono">
              <div>
                <dt className="inline">原始: </dt>
                <dd className="inline" data-testid="generic-diagnostic-raw">
                  {diagnostic.raw}
                </dd>
              </div>
              <div>
                <dt className="inline">归属: </dt>
                <dd className="inline">{diagnostic.owner_id}</dd>
              </div>
            </dl>
          </details>
        )}
      </div>
    </>
  );

  if (interactive) {
    return (
      <button
        type="button"
        data-testid="generic-node-card"
        data-kind={kind}
        data-state="unknown"
        className={base}
        onClick={onOpen}
        aria-label={`未知节点: ${title || nodeId}`}
      >
        {cardInner}
      </button>
    );
  }  return (
    <article
      data-testid="generic-node-card"
      data-kind={kind}
      data-state="unknown"
      className={base}
      aria-label={`未知节点: ${title || nodeId}`}
    >
      {cardInner}
    </article>
  );
}
