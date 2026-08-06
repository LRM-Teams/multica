"use client";

import type { ResearchGraphNode, ResearchSource } from "@multica/core/types";
import { Badge } from "@multica/ui/components/ui/badge";
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
} from "@multica/ui/components/ui/sheet";
import { useIsMobile } from "@multica/ui/hooks/use-mobile";
import { X } from "lucide-react";
import { cn } from "@multica/ui/lib/utils";
import { useT } from "../../i18n/use-t";
import { normalizeNodeStatusKey, visualForNodeType } from "../lib/node-visuals";

const EMPTY_SOURCES: ResearchSource[] = [];

function payloadString(payload: unknown, key: string): string | null {
  if (!payload || typeof payload !== "object") return null;
  const value = (payload as Record<string, unknown>)[key];
  return typeof value === "string" && value.trim() ? value : null;
}

function payloadNumber(payload: unknown, key: string): number | null {
  if (!payload || typeof payload !== "object") return null;
  const value = (payload as Record<string, unknown>)[key];
  return typeof value === "number" && Number.isFinite(value) ? value : null;
}

function typeLabelFor(
  nodeType: string,
  t: ReturnType<typeof useT<"research">>["t"],
): string {
  switch (nodeType) {
    case "goal":
      return t(($) => $.node.goal);
    case "subquestion":
      return t(($) => $.node.subquestion);
    case "probe":
      return t(($) => $.node.probe);
    case "finding":
      return t(($) => $.node.finding);
    case "conflict":
      return t(($) => $.node.conflict);
    case "dead_end":
      return t(($) => $.node.dead_end);
    case "refuted":
      return t(($) => $.node.refuted);
    case "pivot":
      return t(($) => $.node.pivot);
    case "roster_change":
      return t(($) => $.node.roster_change);
    case "stage_gate":
      return t(($) => $.node.stage_gate);
    case "product_round_gate":
      return t(($) => $.node.product_round_gate);
    case "agent_activity":
      return t(($) => $.node.agent_activity);
    default:
      return nodeType;
  }
}

function DetailBody({
  node,
  sources,
  onClose,
  showClose,
}: {
  node: ResearchGraphNode;
  sources: ResearchSource[];
  onClose?: () => void;
  showClose?: boolean;
}) {
  const { t } = useT("research");
  const visual = visualForNodeType(node.node_type);
  const typeLabel = typeLabelFor(node.node_type, t);
  const statusKey = normalizeNodeStatusKey(node.status);
  const statusLabel = t(($) => $.node.status[statusKey]);

  const sourceId = payloadString(node.payload, "source_id");
  const linked = sourceId ? sources.find((s) => s.id === sourceId) : undefined;
  const url = linked?.url || payloadString(node.payload, "url");
  const weight =
    linked?.credibility_weight ?? payloadNumber(node.payload, "credibility_weight");
  const sourceClass = linked?.source_class || payloadString(node.payload, "source_class");
  const confidence =
    payloadNumber(node.payload, "confidence") ??
    payloadNumber(node.payload, "confidence_score");
  const deadEndReason =
    payloadString(node.payload, "reason") ||
    payloadString(node.payload, "dead_end_reason") ||
    (node.node_type === "dead_end" ? node.summary : null);

  const relatedSources = sources
    .filter((s) => {
      if (linked && s.id === linked.id) return true;
      const ids = (node.payload as { source_ids?: unknown } | null)?.source_ids;
      return Array.isArray(ids) && ids.includes(s.id);
    })
    .slice(0, 12);

  const evidenceList =
    relatedSources.length > 0
      ? relatedSources
      : linked
        ? [linked]
        : sources
            .toSorted((a, b) => (b.credibility_weight ?? 0) - (a.credibility_weight ?? 0))
            .slice(0, 6);

  return (
    <>
      <header className="relative border-b px-4 pt-4 pb-3 text-left">
        {showClose ? (
          <button
            type="button"
            onClick={onClose}
            className="absolute top-3 right-3 rounded-md p-1 text-muted-foreground hover:bg-muted hover:text-foreground"
            aria-label={t(($) => $.overlay.detail_close)}
          >
            <X className="size-4" />
          </button>
        ) : null}
        <div className={cn("mb-1 flex flex-wrap items-center gap-2", showClose && "pr-8")}>
          <span className={`h-2 w-2 rounded-full ${visual.accentBarClass}`} />
          <Badge variant="outline" className="text-[10px] uppercase">
            {typeLabel}
          </Badge>
          <Badge variant="secondary" className="text-[10px]">
            {statusLabel}
          </Badge>
          {sourceClass ? (
            <Badge variant="outline" className="text-[10px]">
              {sourceClass}
            </Badge>
          ) : null}
          {typeof weight === "number" ? (
            <Badge variant="secondary" className="text-[10px]">
              {t(($) => $.panel.weight)} {weight.toFixed(2)}
            </Badge>
          ) : null}
          {typeof confidence === "number" ? (
            <Badge variant="secondary" className="text-[10px]">
              {t(($) => $.node.confidence)}{" "}
              {(confidence <= 1 ? confidence * 100 : confidence).toFixed(0)}%
            </Badge>
          ) : null}
        </div>
        <h2 className="text-base leading-snug font-semibold">{node.title}</h2>
        <p className="sr-only">{t(($) => $.node.detail_hint)}</p>
      </header>

      <div className="space-y-4 p-4">
        {node.summary ? (
          <section>
            <h3 className="mb-1 text-[11px] font-semibold tracking-wide text-muted-foreground uppercase">
              {node.status === "active" || node.status === "running"
                ? t(($) => $.node.doing)
                : t(($) => $.node.summary)}
            </h3>
            <p className="whitespace-pre-wrap text-sm leading-relaxed text-foreground">
              {node.summary}
            </p>
          </section>
        ) : (
          <p className="text-sm text-muted-foreground">{t(($) => $.node.summary_empty)}</p>
        )}

        {node.node_type === "dead_end" && deadEndReason ? (
          <section className="rounded-md border border-destructive/30 bg-destructive/5 px-3 py-2">
            <h3 className="mb-1 text-[11px] font-semibold tracking-wide text-destructive uppercase">
              {t(($) => $.node.dead_end_reason)}
            </h3>
            <p className="whitespace-pre-wrap text-sm leading-relaxed">{deadEndReason}</p>
          </section>
        ) : null}

        <section>
          <h3 className="mb-1.5 text-[11px] font-semibold tracking-wide text-muted-foreground uppercase">
            {t(($) => $.node.evidence)}
          </h3>
          {evidenceList.length === 0 ? (
            <p className="text-xs text-muted-foreground">{t(($) => $.node.evidence_empty)}</p>
          ) : (
            <ul className="space-y-2">
              {evidenceList.map((s) => (
                <li key={s.id} className="rounded-md border bg-muted/20 px-2.5 py-2">
                  <div className="flex items-start justify-between gap-2">
                    <a
                      href={s.url}
                      target="_blank"
                      rel="noreferrer"
                      className="min-w-0 truncate text-xs font-medium text-primary underline-offset-2 hover:underline"
                    >
                      {s.title || s.url}
                    </a>
                    <span className="shrink-0 font-mono text-[10px] text-muted-foreground">
                      {(s.credibility_weight ?? 0).toFixed(2)}
                    </span>
                  </div>
                  {s.excerpt ? (
                    <p className="mt-1 line-clamp-3 text-[11px] text-muted-foreground">
                      {s.excerpt}
                    </p>
                  ) : null}
                </li>
              ))}
            </ul>
          )}
        </section>

        {url && !evidenceList.some((s) => s.url === url) ? (
          <a
            href={url}
            target="_blank"
            rel="noreferrer"
            className="block truncate text-[11px] text-primary underline-offset-2 hover:underline"
          >
            {linked?.title || url}
          </a>
        ) : null}
      </div>
    </>
  );
}

/**
 * LRM-797 / LRM-826:
 * - Desktop overlay-card: substantial detail card above Controls (not a tiny chip).
 * - Narrow: full-width bottom sheet.
 */
export function ResearchNodeDetail({
  node,
  sources = EMPTY_SOURCES,
  open = true,
  onClose,
  placement,
}: {
  node: ResearchGraphNode;
  sources?: ResearchSource[];
  open?: boolean;
  onClose?: () => void;
  /** Force placement; default: overlay-card on desktop, sheet on narrow. */
  placement?: "overlay-card" | "sheet";
}) {
  const { t } = useT("research");
  const isMobile = useIsMobile();
  const mode = placement ?? (isMobile ? "sheet" : "overlay-card");

  if (!open) return null;

  if (mode === "overlay-card") {
    return (
      <dialog
        open
        data-testid="research-node-detail"
        data-placement="overlay-card"
        className="relative m-0 flex max-h-[min(52vh,420px)] w-[min(100%,320px)] translate-none flex-col overflow-hidden rounded-xl border bg-card/95 p-0 shadow-lg backdrop-blur-md open:flex"
        aria-label={t(($) => $.node.detail_hint)}
      >
        <div className="min-h-0 flex-1 overflow-y-auto">
          <DetailBody node={node} sources={sources} onClose={onClose} showClose />
        </div>
      </dialog>
    );
  }

  return (
    <Sheet
      open={open}
      onOpenChange={(next) => {
        if (!next) onClose?.();
      }}
    >
      <SheetContent
        side="bottom"
        className="max-h-[90vh] gap-0 overflow-y-auto p-0"
        data-testid="research-node-detail"
        data-placement="sheet"
      >
        <SheetHeader className="sr-only">
          <SheetTitle>{node.title}</SheetTitle>
          <SheetDescription>{t(($) => $.node.detail_hint)}</SheetDescription>
        </SheetHeader>
        <DetailBody node={node} sources={sources} onClose={onClose} />
      </SheetContent>
    </Sheet>
  );
}
