"use client";

import type { ResearchReport, ResearchSource } from "@multica/core/types";
import { ReportReader } from "../report/report-reader";

/**
 * LRM-880: "查看交付 / 来源与报告" opens the HTML report reader (LRM-881 shell).
 * Kept as a named export so session page wiring stays stable.
 */
export function ResearchDeliveryDrawer({
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
