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
  appOrigin,
  onOpenChange,
  onRequestFreshCapability,
  loadTimeoutMs = 15_000,
  pending = false,
}: {
  open: boolean;
  report: ResearchV6ReportSandboxDocument | null;
  appOrigin: string;
  onOpenChange: (open: boolean) => void;
  onRequestFreshCapability?: () => void;
  loadTimeoutMs?: number;
  pending?: boolean;
}) {
  const { t } = useT("research");
  const verdict = validateResearchV6ReportSandboxUrl(
    report?.sandboxUrl ?? "",
    appOrigin,
  );
  const frameIdentity = [
    open ? "open" : "closed",
    report?.id ?? "missing",
    report?.packageHash ?? "missing",
    pending ? "pending" : "settled",
    verdict.ok ? verdict.url : verdict.reason,
  ].join(":");
  const initialPhase: FramePhase = !open
    ? "idle"
    : pending
      ? "loading"
      : verdict.ok
        ? "loading"
        : "unavailable";
  const [frameState, setFrameState] = useState<{
    identity: string;
    phase: FramePhase;
  }>({ identity: frameIdentity, phase: initialPhase });
  if (frameState.identity !== frameIdentity) {
    setFrameState({ identity: frameIdentity, phase: initialPhase });
  }
  const phase =
    frameState.identity === frameIdentity ? frameState.phase : initialPhase;
  const frameUrl = open && verdict.ok && phase !== "unavailable" ? verdict.url : null;

  useEffect(() => {
    if (phase !== "loading" || !frameUrl) return;
    const timer = globalThis.setTimeout(() => {
      setFrameState({ identity: frameIdentity, phase: "unavailable" });
    }, loadTimeoutMs);
    return () => globalThis.clearTimeout(timer);
  }, [frameIdentity, frameUrl, loadTimeoutMs, phase]);

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
