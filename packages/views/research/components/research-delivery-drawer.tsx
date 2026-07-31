"use client";

import type { ResearchReport, ResearchSource } from "@multica/core/types";
import { ReportReader } from "../report/report-reader";

/**
 * LRM-880 / LRM-921: 「查看交付」opens a body-portaled centered HTML reader modal
 * (not a canvas-corner float). Alias kept for session-page wiring stability.
 */
export function ResearchDeliveryDrawer(props: {
  open: boolean;
  onClose: () => void;
  report: ResearchReport | null | undefined;
  sources: ResearchSource[];
  titleFallback?: string;
}) {
  return <ResearchDeliveryModal {...props} />;
}

export function ResearchDeliveryModal({
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
  return (
    <ReportReader
      open={open}
      onClose={onClose}
      report={report}
      sources={sources}
      titleFallback={titleFallback}
    />
  );
}
