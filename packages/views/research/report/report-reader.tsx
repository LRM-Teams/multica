"use client";

import { useEffect, useMemo, useState } from "react";
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

  useEffect(() => {
    if (!open) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") onClose();
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [open, onClose]);

  useEffect(() => {
    if (!open) {
      setOutlineOpen(false);
      setCopied(false);
    }
  }, [open]);

  if (!open) return null;

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

  return (
    <div
      className="pointer-events-auto fixed inset-0 z-50 flex items-stretch justify-center bg-background/70 p-0 backdrop-blur-sm sm:items-center sm:p-6"
      role="dialog"
      aria-modal="true"
      aria-label={t(($) => $.panel.delivery)}
    >
      <div
        className={cn(
          "flex h-full w-full flex-col overflow-hidden border bg-card shadow-2xl",
          "sm:h-[min(900px,calc(100vh-3rem))] sm:max-w-[1120px] sm:rounded-xl",
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
    </div>
  );
}
