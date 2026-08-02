"use client";

import type { ResearchReport, ResearchSource } from "@multica/core/types";
import type { HumanBoundaryModel } from "../lib/m2-visibility";
import { ReportReader } from "../report/report-reader";

/**
 * LRM-880 / LRM-921: 「查看交付」opens a body-portaled centered HTML reader modal
 * (not a canvas-corner float). Alias kept for session-page wiring stability.
 * LRM-993: four-state surface (empty / loading / running / error) lives in ReportReader.
 */
export function ResearchDeliveryDrawer(props: {
  open: boolean;
  onClose: () => void;
  report: ResearchReport | null | undefined;
  sources: ResearchSource[];
  titleFallback?: string;
  /** LRM-890 — shared with session right rail. */
  boundary?: HumanBoundaryModel;
  sessionStatus?: string | null;
  loading?: boolean;
  error?: string | null;
  onRetry?: () => void;
}) {
  return <ResearchDeliveryModal {...props} />;
}

export function ResearchDeliveryModal({
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
  sessionStatus?: string | null;
  loading?: boolean;
  error?: string | null;
  onRetry?: () => void;
}) {
  return (
    <ReportReader
      open={open}
      onClose={onClose}
      report={report}
      sources={sources}
      titleFallback={titleFallback}
      boundary={boundary}
      sessionStatus={sessionStatus}
      loading={loading}
      error={error}
      onRetry={onRetry}
    />
  );
}
