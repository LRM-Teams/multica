"use client";

import { useEffect, useMemo, useState } from "react";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from "@multica/ui/components/ui/dialog";
import { Button } from "@multica/ui/components/ui/button";
import { CheckCircle2, GitMerge, LoaderCircle, ShieldCheck } from "lucide-react";
import type { ResearchV6DirectorReportDirectionCoverage } from "@multica/core/types/research-v6-director";
import { useT } from "../../i18n/use-t";
import { resolveResearchV6ReportFrameSource } from "../lib/research-v6-report-sandbox";

export interface ResearchV6ReportSandboxDocument {
  id: string;
  title: string;
  packageHash: string;
  sandboxUrl: string;
  reportOrigin: string;
  compiledHtml?: string;
  plainTextFallback: string;
  revision?: number;
  status?: string;
  inputCount?: number;
  maturity?: "interim" | "final";
  directionCoverage?: ResearchV6DirectorReportDirectionCoverage[];
  updatedAt?: string;
}

export interface ResearchV6ReportHistoryItem {
  id: string;
  revision: number;
  status: string;
  title: string;
  publishedAt?: string | null;
}

type FramePhase = "idle" | "loading" | "ready" | "empty" | "unavailable";

const EMPTY_REPORT_HISTORY: readonly ResearchV6ReportHistoryItem[] = [];

export function ResearchV6ReportModal({
  open,
  report,
  appOrigin,
  onOpenChange,
  onRequestFreshCapability,
  history = EMPTY_REPORT_HISTORY,
  onSelectReport,
  selectedReportId,
  loading = false,
  updating = false,
  loadTimeoutMs = 15_000,
  presentation = "dialog",
}: {
  open: boolean;
  report: ResearchV6ReportSandboxDocument | null;
  appOrigin: string;
  onOpenChange: (open: boolean) => void;
  onRequestFreshCapability?: () => void;
  history?: readonly ResearchV6ReportHistoryItem[];
  onSelectReport?: (reportId: string) => void;
  selectedReportId?: string | null;
  loading?: boolean;
  updating?: boolean;
  loadTimeoutMs?: number;
  presentation?: "dialog" | "page";
}) {
  const { t } = useT("research");
  const source = resolveResearchV6ReportFrameSource({
    sandboxUrl: report?.sandboxUrl ?? "",
    appOrigin,
    reportOrigin: report?.reportOrigin ?? "",
    compiledHtml: report?.compiledHtml,
  });
  const compiledHtml = source.kind === "compiled" ? source.html : "";
  const compiledBlobUrl = useMemo(() => {
    if (!open || source.kind !== "compiled" || !compiledHtml) {
      return null;
    }
    return URL.createObjectURL(
      new Blob([compiledHtml], { type: "text/html;charset=utf-8" }),
    );
  }, [compiledHtml, open, source.kind]);
  useEffect(() => {
    if (!compiledBlobUrl) return;
    return () => {
      URL.revokeObjectURL(compiledBlobUrl);
    };
  }, [compiledBlobUrl]);
  const frameIdentity = [
    open ? "open" : "closed",
    report?.id ?? "missing",
    report?.packageHash ?? "missing",
    loading ? "fetching" : "settled",
    source.kind,
    source.kind === "isolated"
      ? source.url
      : source.kind === "compiled"
        ? String(compiledHtml.length)
        : source.reason,
  ].join(":");
  const initialPhase: FramePhase = !open
    ? "idle"
    : loading
      ? "loading"
      : !report
        ? "empty"
        : source.kind === "unavailable"
          ? "unavailable"
          : "loading";
  const [frameState, setFrameState] = useState<{
    identity: string;
    phase: FramePhase;
  }>({ identity: frameIdentity, phase: initialPhase });
  useEffect(() => {
    if (frameState.identity !== frameIdentity) {
      setFrameState({ identity: frameIdentity, phase: initialPhase });
    }
  }, [frameState.identity, frameIdentity, initialPhase]);
  const phase =
    frameState.identity === frameIdentity ? frameState.phase : initialPhase;
  const isolatedUrl = source.kind === "isolated" ? source.url : null;
  const frameUrl =
    !open || phase === "empty" || phase === "unavailable"
      ? null
      : isolatedUrl ?? (source.kind === "compiled" ? compiledBlobUrl : null);
  const documentLabel =
    source.kind === "compiled"
      ? t(($) => $.d5.report_sandbox.sandboxed_document)
      : t(($) => $.d5.report_sandbox.isolated_document);
  const loadingLabel =
    source.kind === "compiled"
      ? t(($) => $.d5.report_sandbox.loading_document)
      : t(($) => $.d5.report_sandbox.loading);

  useEffect(() => {
    if (phase !== "loading" || !frameUrl) return;
    const timer = globalThis.setTimeout(() => {
      setFrameState({ identity: frameIdentity, phase: "unavailable" });
    }, loadTimeoutMs);
    return () => globalThis.clearTimeout(timer);
  }, [frameIdentity, frameUrl, loadTimeoutMs, phase]);

  useEffect(() => {
    if (!frameUrl || (phase !== "loading" && phase !== "ready")) return;
    const PerformanceObserverType = globalThis.PerformanceObserver;
    if (!PerformanceObserverType) return;
    let blockedDuration = 0;
    let longTaskCount = 0;
    const observer = new PerformanceObserverType((list) => {
      for (const entry of list.getEntries()) {
        blockedDuration += entry.duration;
        longTaskCount += 1;
      }
      if (blockedDuration >= 2_500 || longTaskCount >= 8) {
        observer.disconnect();
        setFrameState({ identity: frameIdentity, phase: "unavailable" });
      }
    });
    try {
      observer.observe({ entryTypes: ["longtask"] });
    } catch {
      observer.disconnect();
    }
    return () => observer.disconnect();
  }, [frameIdentity, frameUrl, phase]);

  const unavailable = phase === "unavailable";
  const fallback = report?.plainTextFallback.trim() ?? "";
  const updatedLabel = report?.updatedAt
    ? `${new Date(report.updatedAt).toISOString().slice(0, 16).replace("T", " ")} UTC`
    : "";
  const reportNotice = updating
    ? t(($) => $.d5.report_sandbox.updating)
    : report?.maturity === "final"
      ? t(($) => $.d5.report_sandbox.final_notice)
      : t(($) => $.d5.report_sandbox.interim_notice);

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent
        className={
          presentation === "page"
            ? "flex h-dvh w-dvw max-w-none flex-col gap-0 overflow-hidden rounded-none border-0 p-0 sm:h-dvh sm:w-dvw"
            : "flex h-[calc(100dvh-1rem)] w-[calc(100vw-1rem)] max-w-none flex-col gap-0 overflow-hidden rounded-xl p-0 sm:h-[min(94vh,1000px)] sm:w-[min(96vw,1600px)]"
        }
        data-testid="research-v6-report-modal"
      >
        <DialogHeader className="shrink-0 border-b border-border/70 bg-card px-4 py-3 text-left sm:px-5">
          <div className="flex min-w-0 items-start justify-between gap-4 pr-8">
            <div className="min-w-0">
              <DialogTitle className="truncate text-base sm:text-lg">
                {report?.title || t(($) => $.d5.report_sandbox.title)}
              </DialogTitle>
              {report ? (
                <DialogDescription className="mt-1 flex min-w-0 items-center gap-1.5 truncate text-[11px]">
                  <ShieldCheck className="size-3 shrink-0 text-success" aria-hidden="true" />
                  <span className="truncate">
                    {documentLabel}
                    {report.packageHash ? ` · ${report.packageHash}` : ""}
                  </span>
                </DialogDescription>
              ) : null}
              {report?.revision || report?.status || report?.inputCount != null ? (
                <p className="mt-1.5 truncate text-[10px] text-muted-foreground">
                  {report.revision
                    ? t(($) => $.d5.report_sandbox.revision_label, {
                        revision: report.revision,
                      })
                    : null}
                  {report.status ? ` · ${report.status}` : ""}
                  {report.inputCount != null
                    ? ` · ${t(($) => $.d5.report_sandbox.input_count, {
                        count: report.inputCount,
                      })}`
                    : ""}
                  {updatedLabel
                    ? ` · ${t(($) => $.d5.report_sandbox.updated_at, {
                        time: updatedLabel,
                      })}`
                    : ""}
                </p>
              ) : null}
            </div>
            {history.length > 1 && onSelectReport ? (
              <label className="flex shrink-0 items-center gap-2 text-[11px] text-muted-foreground">
                <span>{t(($) => $.d5.report_sandbox.revision_history)}</span>
                <select
                  className="h-8 max-w-48 rounded-lg border border-border bg-background px-2 text-xs text-foreground"
                  value={selectedReportId ?? report?.id ?? ""}
                  onChange={(event) => onSelectReport(event.target.value)}
                >
                  {history.map((item) => (
                    <option key={item.id} value={item.id}>
                      {t(($) => $.d5.report_sandbox.revision_option, {
                        revision: item.revision,
                        status: item.status,
                      })}
                    </option>
                  ))}
                </select>
              </label>
            ) : null}
          </div>
        </DialogHeader>

        {report ? (
          <div className="flex shrink-0 flex-wrap items-center gap-x-4 gap-y-2 border-b border-border/70 bg-muted/35 px-4 py-2.5 text-xs sm:px-5">
            <div className="flex items-center gap-2 font-medium text-foreground">
              {report.maturity === "final" ? (
                <CheckCircle2 className="size-3.5 text-success" aria-hidden="true" />
              ) : (
                <GitMerge className="size-3.5 text-primary" aria-hidden="true" />
              )}
              <span>
                {report.maturity === "final"
                  ? t(($) => $.d5.report_sandbox.final_report)
                  : t(($) => $.d5.report_sandbox.interim_report)}
              </span>
            </div>
            <p className="text-muted-foreground">
              {reportNotice}
            </p>
            {report.directionCoverage?.length ? (
              <div className="ml-auto flex max-w-full items-center gap-1.5 overflow-x-auto pb-0.5">
                {report.directionCoverage.map((direction) => (
                  <span
                    key={direction.branchId}
                    className="inline-flex shrink-0 items-center gap-1 rounded-md border border-border/70 bg-background px-2 py-1 text-[10px] text-muted-foreground"
                    title={direction.objective}
                  >
                    <span className="max-w-36 truncate">{direction.objective}</span>
                    {direction.tier ? (
                      <span className="font-semibold tabular-nums text-foreground">
                        {direction.tier}
                      </span>
                    ) : (
                      <span>{t(($) => $.d5.report_sandbox.researching)}</span>
                    )}
                    {direction.activeWorkCount > 0 ? (
                      <span className="text-primary">
                        {t(($) => $.d5.report_sandbox.active_tasks, {
                          count: direction.activeWorkCount,
                        })}
                      </span>
                    ) : null}
                  </span>
                ))}
              </div>
            ) : null}
          </div>
        ) : null}

        <div className="relative min-h-0 flex-1 bg-background">
          {frameUrl ? (
            <iframe
              key={`${report?.id ?? "report"}:${report?.packageHash ?? ""}`}
              className="absolute inset-0 size-full border-0 bg-background"
              data-testid="research-v6-report-frame"
              loading="eager"
              onError={() => {
                setFrameState({ identity: frameIdentity, phase: "unavailable" });
              }}
              onLoad={() =>
                setFrameState({ identity: frameIdentity, phase: "ready" })
              }
              referrerPolicy="no-referrer"
              sandbox="allow-scripts"
              src={frameUrl}
              title={report?.title || t(($) => $.d5.report_sandbox.frame_title)}
            />
          ) : null}

          {phase === "loading" ? (
            <div
              className="absolute inset-0 grid place-items-center bg-background text-foreground"
              data-testid="research-v6-report-loading"
            >
              <div className="flex items-center gap-2 text-sm">
                <LoaderCircle
                  className="size-4 animate-spin motion-reduce:animate-none"
                  aria-hidden="true"
                />
                {loadingLabel}
              </div>
            </div>
          ) : null}

          {phase === "empty" ? (
            <div
              className="absolute inset-0 grid place-items-center bg-card px-5 py-8 sm:px-10 sm:py-12"
              data-testid="research-v6-report-empty"
            >
              <div className="max-w-md text-center">
                <h2 className="text-balance text-xl font-semibold">
                  {updating
                    ? t(($) => $.d5.report_sandbox.building_title)
                    : t(($) => $.d5.report_sandbox.empty_title)}
                </h2>
                <p className="mt-2 text-sm leading-6 text-muted-foreground">
                  {updating
                    ? t(($) => $.d5.report_sandbox.building_body)
                    : t(($) => $.d5.report_sandbox.empty_body)}
                </p>
              </div>
            </div>
          ) : null}

          {unavailable ? (
            <div className="absolute inset-0 overflow-y-auto bg-card px-5 py-8 sm:px-10 sm:py-12">
              <div className="mx-auto max-w-3xl">
                <h2 className="text-balance text-xl font-semibold">
                  {t(($) => $.d5.report_sandbox.unavailable_title)}
                </h2>
                <p className="mt-2 text-sm leading-6 text-muted-foreground">
                  {t(($) => $.d5.report_sandbox.unavailable_body)}
                </p>
                {report && onRequestFreshCapability ? (
                  <Button
                    type="button"
                    variant="outline"
                    className="mt-5"
                    onClick={onRequestFreshCapability}
                  >
                    {t(($) => $.d5.report_sandbox.refresh_capability)}
                  </Button>
                ) : null}
                {fallback ? (
                  <section className="mt-8 border-t border-border/70 pt-6">
                    <h3 className="text-sm font-semibold">
                      {t(($) => $.d5.report_sandbox.plain_text_title)}
                    </h3>
                    <p className="mt-3 whitespace-pre-wrap text-sm leading-7 text-foreground/90">
                      {fallback}
                    </p>
                  </section>
                ) : null}
              </div>
            </div>
          ) : null}
        </div>
      </DialogContent>
    </Dialog>
  );
}
