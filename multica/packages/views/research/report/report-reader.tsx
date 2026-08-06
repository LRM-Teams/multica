"use client";

import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { createPortal } from "react-dom";
import { toast } from "sonner";
import { Copy, Download, List, X } from "lucide-react";
import { normalizeReportStructured } from "@multica/core/research";
import type { ResearchReport, ResearchSource } from "@multica/core/types";
import { Button } from "@multica/ui/components/ui/button";
import { cn } from "@multica/ui/lib/utils";
import { useT } from "../../i18n/use-t";
import { HumanBoundaryCard } from "../components/human-boundary-card";
import {
  ResearchDeliveryModeBody,
  ResearchDeliveryModeChip,
} from "../components/research-delivery-mode-body";
import {
  deliveryContentCount,
  resolveDeliveryMode,
} from "../lib/delivery-mode";
import type { HumanBoundaryModel } from "../lib/m2-visibility";
import {
  buildOutlineItems,
  outlineSectionDomId,
  resolveActiveOutlineId,
  type OutlineItem,
} from "../lib/report-outline";
import { buildReportMarkdown } from "./report-markdown";
import { ReportProse } from "./report-prose";
import { ReportSourceTable } from "./report-source-table";
import { ReportSourcesFailureBanner } from "./report-sources-failure-banner";
import {
  partitionSourcesByFailure,
  resolveSourcesFailureMode,
} from "./report-source-degrade";
import { citationAnchorId } from "./report-citation-resolve";

/** LRM-824 — smooth scroll + ~1s highlight fade; never touches the history
 * stack (no location.hash / pushState). Module scope: no local state. */
function anchorTarget(el: HTMLElement | null) {
  if (!el) return;
  el.scrollIntoView({ behavior: "smooth", block: "start" });
  el.classList.add("research-anchor-flash");
  window.setTimeout(() => el.classList.remove("research-anchor-flash"), 1000);
}

function OutlineNav({
  items,
  activeId,
  onPick,
  className,
}: {
  items: OutlineItem[];
  activeId?: string | null;
  onPick: (id: string) => void;
  className?: string;
}) {
  const { t } = useT("research");
  return (
    <nav
      aria-label={t(($) => $.reader.outline)}
      data-testid="research-report-outline"
      className={cn("space-y-0.5", className)}
    >
      <div className="mb-2 px-2 text-[11px] font-medium tracking-wide text-muted-foreground uppercase">
        {t(($) => $.reader.outline)}
      </div>
      {items.length === 0 ? (
        <p className="px-2 text-xs text-muted-foreground">—</p>
      ) : (
        items.map((item) => {
          const active = activeId === item.id;
          return (
            <button
              key={item.id}
              type="button"
              data-outline-id={item.id}
              data-outline-level={item.level}
              data-outline-active={active ? "true" : "false"}
              aria-current={active ? "true" : undefined}
              onClick={() => onPick(item.id)}
              className={cn(
                "block w-full truncate rounded-md px-2 py-1.5 text-left transition-colors",
                item.level <= 1 && "text-sm font-medium",
                item.level === 2 && "pl-4 text-[13px]",
                item.level >= 3 && "pl-7 text-xs",
                active
                  ? "bg-brand/10 font-medium text-foreground"
                  : "text-muted-foreground hover:bg-muted/60 hover:text-foreground",
              )}
            >
              {item.title}
            </button>
          );
        })
      )}
    </nav>
  );
}

export function ReportReader({
  open,
  onClose,
  report,
  sources,
  titleFallback,
  boundary,
  sessionStatus,
  loading,
  error,
  onRetry,
}: {
  open: boolean;
  onClose: () => void;
  report: ResearchReport | null | undefined;
  sources: ResearchSource[];
  titleFallback?: string;
  boundary?: HumanBoundaryModel;
  /** LRM-993 — drives empty vs in-flight loading. */
  sessionStatus?: string | null;
  loading?: boolean;
  error?: string | null;
  onRetry?: () => void;
}) {
  const { t } = useT("research");
  const dialogRef = useRef<HTMLDialogElement | null>(null);
  const scrollRef = useRef<HTMLDivElement | null>(null);
  const [outlineOpen, setOutlineOpen] = useState(false);
  const [activeId, setActiveId] = useState<string | null>(null);
  const [copied, setCopied] = useState(false);

  const contentCount = deliveryContentCount(report, sources.length);
  const mode = resolveDeliveryMode(contentCount, sessionStatus, {
    loading,
    error,
  });
  const showReaderChrome =
    mode === "running" || (mode === "error" && contentCount > 0);

  const normalized = useMemo(
    () => normalizeReportStructured(report?.structured),
    [report?.structured],
  );

  const outlineItems = useMemo(
    () =>
      buildOutlineItems(normalized, {
        sources: t(($) => $.reader.sources_heading),
        body: t(($) => $.panel.report),
      }),
    [normalized, t],
  );

  const sourcesFailureMode = useMemo(
    () => resolveSourcesFailureMode(sources),
    [sources],
  );
  const failedSourceCount = useMemo(
    () => partitionSourcesByFailure(sources).failed.length,
    [sources],
  );

  const title =
    (normalized.structured?.title && normalized.structured.title.trim()) ||
    titleFallback ||
    t(($) => $.panel.delivery);

  // Reset transient UI when the panel closes — adjust during render (prev-prop).
  const prevOpenRef = useRef(open);
  if (open !== prevOpenRef.current) {
    prevOpenRef.current = open;
    if (!open) {
      setOutlineOpen(false);
      setCopied(false);
      setActiveId(null);
    }
  }

  const bindDialog = useCallback((dialog: HTMLDialogElement | null) => {
    dialogRef.current = dialog;
    if (!dialog || dialog.open) return;
    if (typeof dialog.showModal === "function") dialog.showModal();
    else dialog.setAttribute("open", "");
  }, []);

  // LRM-829 — scroll spy: highlight the outline item for the section in view.
  useEffect(() => {
    if (!open || !showReaderChrome) return;
    const root = scrollRef.current;
    if (!root) return;

    const syncActive = () => {
      const offsets = outlineItems
        .map((item) => {
          const el = document.getElementById(outlineSectionDomId(item.id));
          if (!el) return null;
          // offset relative to the scroll container
          const offsetTop =
            el.getBoundingClientRect().top -
            root.getBoundingClientRect().top +
            root.scrollTop;
          return { id: item.id, offsetTop };
        })
        .filter((v): v is { id: string; offsetTop: number } => Boolean(v));
      const next = resolveActiveOutlineId(root.scrollTop, offsets);
      if (next) setActiveId(next);
    };

    syncActive();
    root.addEventListener("scroll", syncActive, { passive: true });
    return () => root.removeEventListener("scroll", syncActive);
  }, [open, showReaderChrome, outlineItems]);

  const scrollTo = useCallback((id: string) => {
    setActiveId(id);
    setOutlineOpen(false);
    const el = document.getElementById(outlineSectionDomId(id));
    anchorTarget(el);
  }, []);

  const locateCitation = useCallback((citationId: string) => {
    setOutlineOpen(false);
    anchorTarget(document.getElementById(citationAnchorId(citationId)));
  }, []);

  const copyMarkdown = async () => {
    const md = buildReportMarkdown(report);
    if (!md) return;
    try {
      await navigator.clipboard.writeText(md);
      setCopied(true);
      window.setTimeout(() => setCopied(false), 1600);
      toast.success(t(($) => $.reader.copy_md_done));
    } catch {
      // clipboard may be denied in headless — ignore
    }
  };

  const exportMarkdown = () => {
    const md = buildReportMarkdown(report);
    if (!md) return;
    const blob = new Blob([md], { type: "text/markdown;charset=utf-8" });
    const url = URL.createObjectURL(blob);
    const a = document.createElement("a");
    a.href = url;
    a.download = "research-report.md";
    a.click();
    URL.revokeObjectURL(url);
    toast.success(t(($) => $.reader.export_done));
  };

  if (!open || typeof document === "undefined") return null;

  // Portaled to body so canvas `relative`/`overflow` cannot pin this into a
  // corner float (LRM-880 / LRM-921 anti-example).
  return createPortal(
    <dialog
      ref={bindDialog}
      data-testid="research-delivery-modal"
      className={cn(
        "fixed inset-0 z-[80] m-0 flex h-dvh max-h-none w-screen max-w-none items-stretch justify-center border-0 bg-transparent p-0 open:flex sm:items-center sm:p-6 md:p-8",
        "backdrop:bg-black/55 backdrop:backdrop-blur-[2px]",
      )}
      aria-label={t(($) => $.panel.delivery)}
      aria-modal="true"
      onCancel={(event) => {
        event.preventDefault();
        const dialog = dialogRef.current;
        if (dialog?.open) {
          if (typeof dialog.close === "function") dialog.close();
          else dialog.removeAttribute("open");
        }
        onClose();
      }}
      onClose={onClose}
    >
      <button
        type="button"
        className="absolute inset-0 z-0 cursor-default bg-transparent"
        aria-label={t(($) => $.panel.hide_chat)}
        onClick={onClose}
      />
      <div
        role="document"
        data-testid="research-delivery-modal-card"
        data-delivery-mode={mode}
        className={cn(
          "relative z-10 flex h-full w-full flex-col overflow-hidden border bg-card shadow-2xl",
          // Desktop: dominant reading region (not a 420px corner chip).
          "sm:h-[min(920px,calc(100vh-4rem))] sm:w-full sm:max-w-[min(1120px,calc(100vw-4rem))] sm:rounded-2xl",
        )}
      >
        <header className="flex shrink-0 flex-wrap items-center gap-2 border-b px-3 py-2.5 sm:px-4">
          {showReaderChrome ? (
            <Button
              type="button"
              size="sm"
              variant="ghost"
              className="sm:hidden"
              data-testid="research-report-outline-toggle"
              onClick={() => setOutlineOpen((v) => !v)}
              aria-expanded={outlineOpen}
              aria-label={t(($) => $.reader.outline)}
            >
              <List className="size-4" />
            </Button>
          ) : null}
          <div className="min-w-0 flex-1">
            <div className="flex min-w-0 flex-wrap items-center gap-1.5">
              <h2 className="truncate text-sm font-semibold sm:text-base">{title}</h2>
              <ResearchDeliveryModeChip mode={mode} />
            </div>
            <p className="truncate text-[11px] text-muted-foreground">
              {t(($) => $.reader.meta, {
                revision: report?.revision ?? 1,
                count: sources.length,
              })}
            </p>
          </div>
          {showReaderChrome ? (
            <>
              <Button
                type="button"
                size="sm"
                variant="outline"
                onClick={() => void copyMarkdown()}
              >
                <Copy className="size-3.5" />
                {copied ? t(($) => $.reader.copied) : t(($) => $.reader.copy_md)}
              </Button>
              <Button type="button" size="sm" onClick={exportMarkdown}>
                <Download className="size-3.5" />
                {t(($) => $.reader.export)}
              </Button>
            </>
          ) : null}
          <Button
            type="button"
            size="icon-sm"
            variant="ghost"
            onClick={onClose}
            aria-label={t(($) => $.panel.hide_chat)}
          >
            <X className="size-4" />
          </Button>
        </header>

        {/* LRM-829 — narrow: outline folds into a top drawer under the header. */}
        {showReaderChrome && outlineOpen ? (
          <div
            data-testid="research-report-outline-drawer"
            className="max-h-[40vh] overflow-y-auto border-b bg-muted/20 px-2 py-2 sm:hidden"
          >
            <OutlineNav items={outlineItems} activeId={activeId} onPick={scrollTo} />
          </div>
        ) : null}

        {mode === "empty" || mode === "loading" || (mode === "error" && contentCount <= 0) ? (
          <div className="min-h-0 flex-1 overflow-y-auto px-4 py-5 sm:px-8 sm:py-6">
            <ResearchDeliveryModeBody
              mode={mode === "error" ? "error" : mode}
              errorMessage={typeof error === "string" ? error : null}
              onRetry={onRetry}
            />
          </div>
        ) : (
          <div className="flex min-h-0 flex-1">
            <aside
              data-testid="research-report-outline-aside"
              className="hidden w-[220px] shrink-0 overflow-y-auto border-r p-3 sm:block"
            >
              <OutlineNav items={outlineItems} activeId={activeId} onPick={scrollTo} />
            </aside>
            <div
              ref={scrollRef}
              data-testid="research-report-scroll"
              className="min-h-0 flex-1 overflow-y-auto px-4 py-5 sm:px-8 sm:py-6"
            >
              {mode === "error" ? (
                <div className="mb-4">
                  <ResearchDeliveryModeBody
                    mode="error"
                    errorMessage={typeof error === "string" ? error : null}
                    onRetry={onRetry}
                  />
                </div>
              ) : null}
              <div id="report-body" className="scroll-mt-4">
                <ReportProse
                  report={report}
                  sources={sources}
                  onLocateCitation={locateCitation}
                />
              </div>
              {boundary ? (
                <div className="mt-8 scroll-mt-4">
                  <HumanBoundaryCard model={boundary} embedded />
                </div>
              ) : null}
              <section id="report-sources" className="mt-10 scroll-mt-4 space-y-3">
                <h2 className="border-t pt-5 text-lg font-semibold">
                  {t(($) => $.reader.sources_heading)}
                </h2>
                <p className="text-sm text-muted-foreground">
                  {t(($) => $.reader.sources_hint)}
                </p>
                <ReportSourcesFailureBanner
                  mode={sourcesFailureMode}
                  failedCount={failedSourceCount}
                  onRetry={onRetry}
                />
                <ReportSourceTable sources={sources} />
              </section>
            </div>
          </div>
        )}
      </div>
    </dialog>,
    document.body,
  );
}
