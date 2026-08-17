"use client";

import { useEffect, useState } from "react";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from "@multica/ui/components/ui/dialog";
import { Button } from "@multica/ui/components/ui/button";
import { LoaderCircle } from "lucide-react";
import { useT } from "../../i18n/use-t";
import { validateResearchV6ReportSandboxUrl } from "../lib/research-v6-report-sandbox";

export interface ResearchV6ReportSandboxDocument {
  id: string;
  title: string;
  packageHash: string;
  sandboxUrl: string;
  plainTextFallback: string;
}

type FramePhase = "idle" | "loading" | "ready" | "unavailable";

export function ResearchV6ReportModal({
  open,
  report,
  onOpenChange,
  onRequestFreshCapability,
  loadTimeoutMs = 15_000,
}: {
  open: boolean;
  report: ResearchV6ReportSandboxDocument | null;
  onOpenChange: (open: boolean) => void;
  onRequestFreshCapability?: () => void;
  loadTimeoutMs?: number;
}) {
  const { t } = useT("research");
  const [frameUrl, setFrameUrl] = useState<string | null>(null);
  const [phase, setPhase] = useState<FramePhase>("idle");
  const reportId = report?.id ?? null;
  const reportPackageHash = report?.packageHash ?? null;
  const reportSandboxUrl = report?.sandboxUrl ?? null;

  useEffect(() => {
    if (!open) {
      setFrameUrl(null);
      setPhase("idle");
      return;
    }
    if (!reportSandboxUrl) {
      setFrameUrl(null);
      setPhase("unavailable");
      return;
    }
    const verdict = validateResearchV6ReportSandboxUrl(
      reportSandboxUrl,
      globalThis.location?.origin ?? "",
    );
    if (!verdict.ok) {
      setFrameUrl(null);
      setPhase("unavailable");
      return;
    }
    setFrameUrl(verdict.url);
    setPhase("loading");
  }, [open, reportId, reportPackageHash, reportSandboxUrl]);

  useEffect(() => {
    if (phase !== "loading" || !frameUrl) return;
    const timer = globalThis.setTimeout(() => {
      setFrameUrl(null);
      setPhase("unavailable");
    }, loadTimeoutMs);
    return () => globalThis.clearTimeout(timer);
  }, [frameUrl, loadTimeoutMs, phase]);

  const unavailable = phase === "unavailable";
  const fallback = report?.plainTextFallback.trim() ?? "";

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent
        className="flex h-[calc(100dvh-1rem)] w-[calc(100vw-1rem)] max-w-none flex-col gap-0 overflow-hidden rounded-xl p-0 sm:h-[min(94vh,1000px)] sm:w-[min(96vw,1600px)]"
        data-testid="research-v6-report-modal"
      >
        <DialogHeader className="shrink-0 border-b border-border/70 bg-card px-4 py-3 text-left sm:px-5">
          <DialogTitle className="truncate pr-8 text-base sm:text-lg">
            {report?.title || t(($) => $.d5.report_sandbox.title)}
          </DialogTitle>
          <DialogDescription className="truncate text-[11px]">
            {t(($) => $.d5.report_sandbox.isolated_document)}
            {report?.packageHash ? ` · ${report.packageHash}` : ""}
          </DialogDescription>
        </DialogHeader>

        <div className="relative min-h-0 flex-1 bg-[#07111b]">
          {frameUrl ? (
            <iframe
              key={`${report?.id ?? "report"}:${report?.packageHash ?? ""}`}
              className="absolute inset-0 size-full border-0 bg-background"
              data-testid="research-v6-report-frame"
              loading="eager"
              onError={() => {
                setFrameUrl(null);
                setPhase("unavailable");
              }}
              onLoad={() => setPhase("ready")}
              referrerPolicy="no-referrer"
              sandbox="allow-scripts"
              src={frameUrl}
              title={report?.title || t(($) => $.d5.report_sandbox.frame_title)}
            />
          ) : null}

          {phase === "loading" ? (
            <div
              className="absolute inset-0 grid place-items-center bg-[#07111b] text-slate-200"
              data-testid="research-v6-report-loading"
            >
              <div className="flex items-center gap-2 text-sm">
                <LoaderCircle
                  className="size-4 animate-spin motion-reduce:animate-none"
                  aria-hidden="true"
                />
                {t(($) => $.d5.report_sandbox.loading)}
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
                {onRequestFreshCapability ? (
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
