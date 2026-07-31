"use client";

import { useCallback, useMemo, useRef, useState } from "react";
import { createPortal } from "react-dom";
import { Copy, Download, List, X } from "lucide-react";
import { normalizeReportStructured } from "@multica/core/research";
import type { ResearchReport, ResearchSource } from "@multica/core/types";
import { Button } from "@multica/ui/components/ui/button";
import { cn } from "@multica/ui/lib/utils";
import { useT } from "../../i18n/use-t";
import { ReportProse } from "./report-prose";
import { ReportSourceTable } from "./report-source-table";

function OutlineNav({
  items,
  activeId,
  onPick,
  className,
}: {
  items: { id: string; title: string; level: number }[];
  activeId?: string | null;
  onPick: (id: string) => void;
  className?: string;
}) {
  const { t } = useT("research");
  return (
    <nav aria-label={t(($) => $.reader.outline)} className={cn("space-y-0.5", className)}>
      <div className="mb-2 px-2 text-[11px] font-medium tracking-wide text-muted-foreground uppercase">
        {t(($) => $.reader.outline)}
      </div>
      {items.length === 0 ? (
        <p className="px-2 text-xs text-muted-foreground">—</p>
      ) : (
        items.map((item) => (
          <button
            key={item.id}
            type="button"
            onClick={() => onPick(item.id)}
            className={cn(
              "block w-full truncate rounded-md px-2 py-1.5 text-left text-sm transition-colors",
              item.level > 1 && "pl-4 text-muted-foreground",
              activeId === item.id
                ? "bg-brand/10 font-medium text-foreground"
                : "text-muted-foreground hover:bg-muted/60 hover:text-foreground",
            )}
          >
            {item.title}
          </button>
        ))
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
}: {
  open: boolean;
  onClose: () => void;
  report: ResearchReport | null | undefined;
  sources: ResearchSource[];
  titleFallback?: string;
}) {
  const { t } = useT("research");
  const dialogRef = useRef<HTMLDialogElement | null>(null);
  const [outlineOpen, setOutlineOpen] = useState(false);
  const [activeId, setActiveId] = useState<string | null>(null);
  const [copied, setCopied] = useState(false);

  const normalized = useMemo(
    () => normalizeReportStructured(report?.structured),
    [report?.structured],
  );

  const outlineItems = useMemo(() => {
    if (normalized.render_mode === "structured" && normalized.structured) {
      return normalized.structured.outline.map((n) => ({
        id: n.id,
        title: n.title,
        level: n.level,
      }));
    }
    return [
      { id: "sources", title: t(($) => $.reader.sources_heading), level: 1 },
      { id: "body", title: t(($) => $.panel.report), level: 1 },
    ];
  }, [normalized, t]);

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
    }
  }

  const bindDialog = useCallback((dialog: HTMLDialogElement | null) => {
    dialogRef.current = dialog;
    if (!dialog || dialog.open) return;
    if (typeof dialog.showModal === "function") dialog.showModal();
    else dialog.setAttribute("open", "");
  }, []);

  if (!open || typeof document === "undefined") return null;

  const scrollTo = (id: string) => {
    setActiveId(id);
    setOutlineOpen(false);
    const el =
      document.getElementById(`report-sec-${id}`) ||
      document.getElementById(`report-${id}`);
    el?.scrollIntoView({ behavior: "smooth", block: "start" });
  };

  const copyMarkdown = async () => {
    const md = report?.content_md?.trim();
    if (!md) return;
    try {
      await navigator.clipboard.writeText(md);
      setCopied(true);
      window.setTimeout(() => setCopied(false), 1600);
    } catch {
      // clipboard may be denied in headless — ignore
    }
  };

  const exportMarkdown = () => {
    const md = report?.content_md ?? "";
    const blob = new Blob([md], { type: "text/markdown;charset=utf-8" });
    const url = URL.createObjectURL(blob);
    const a = document.createElement("a");
    a.href = url;
    a.download = "research-report.md";
    a.click();
    URL.revokeObjectURL(url);
  };

  // Portaled to body so canvas `relative`/`overflow` cannot pin this into a
  // corner float (LRM-880 / LRM-921 anti-example).
  return createPortal(
    <dialog
      ref={bindDialog}
      data-testid="research-delivery-modal"
      className={cn(
        "fixed inset-0 z-[80] m-0 flex h-dvh max-h-none w-screen max-w-none items-stretch justify-center border-0 bg-black/45 p-0 open:flex sm:items-center sm:p-6 md:p-8",
        "backdrop:bg-black/45",
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
      <div
        data-testid="research-delivery-modal-card"
        className={cn(
          "flex h-full w-full flex-col overflow-hidden border bg-card shadow-2xl",
          // Desktop: dominant reading region (not a 420px corner chip).
          "sm:h-[min(920px,calc(100vh-4rem))] sm:max-w-[min(1120px,calc(100vw-4rem))] sm:rounded-xl",
        )}
      >
        <header className="flex shrink-0 flex-wrap items-center gap-2 border-b px-3 py-2.5 sm:px-4">
          <Button
            type="button"
            size="sm"
            variant="ghost"
            className="sm:hidden"
            onClick={() => setOutlineOpen((v) => !v)}
            aria-expanded={outlineOpen}
            aria-label={t(($) => $.reader.outline)}
          >
            <List className="size-4" />
          </Button>
          <div className="min-w-0 flex-1">
            <h2 className="truncate text-sm font-semibold sm:text-base">{title}</h2>
            <p className="truncate text-[11px] text-muted-foreground">
              {t(($) => $.reader.meta, {
                revision: report?.revision ?? 1,
                count: sources.length,
              })}
            </p>
          </div>
          <Button type="button" size="sm" variant="outline" onClick={() => void copyMarkdown()}>
            <Copy className="size-3.5" />
            {copied ? t(($) => $.reader.copied) : t(($) => $.reader.copy_md)}
          </Button>
          <Button type="button" size="sm" onClick={exportMarkdown}>
            <Download className="size-3.5" />
            {t(($) => $.reader.export)}
          </Button>
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

        {outlineOpen ? (
          <div className="border-b bg-muted/20 px-2 py-2 sm:hidden">
            <OutlineNav items={outlineItems} activeId={activeId} onPick={scrollTo} />
          </div>
        ) : null}

        <div className="flex min-h-0 flex-1">
          <aside className="hidden w-[220px] shrink-0 overflow-y-auto border-r p-3 sm:block">
            <OutlineNav items={outlineItems} activeId={activeId} onPick={scrollTo} />
          </aside>
          <div className="min-h-0 flex-1 overflow-y-auto px-4 py-5 sm:px-8 sm:py-6">
            <div id="report-body" className="scroll-mt-4">
              <ReportProse report={report} />
            </div>
            <section id="report-sources" className="mt-10 scroll-mt-4 space-y-3">
              <h2 className="border-t pt-5 text-lg font-semibold">
                {t(($) => $.reader.sources_heading)}
              </h2>
              <p className="text-sm text-muted-foreground">
                {t(($) => $.reader.sources_hint)}
              </p>
              <ReportSourceTable sources={sources} />
            </section>
          </div>
        </div>
      </div>
    </dialog>,
    document.body,
  );
}
