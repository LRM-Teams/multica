"use client";

import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import {
  researchV6DirectorReportCompiledOptions,
  researchV6DirectorReportOptions,
  researchV6DirectorReportsOptions,
} from "@multica/core/research-v6/director-queries";
import type { ResearchV6DirectorDetailTransport } from "@multica/core/types/research-v6-director";
import { validateResearchV6ReportSandboxUrl } from "../lib/research-v6-report-sandbox";

export interface ResearchV6ReportsInput {
  enabled: boolean;
  deliveryOpen: boolean;
  workspaceId: string;
  runId: string;
  transport: ResearchV6DirectorDetailTransport;
}

/** Owns Director report selection, safe sandbox resolution, and HTML fallback. */
export function useResearchV6Reports({
  enabled,
  deliveryOpen,
  workspaceId,
  runId,
  transport,
}: ResearchV6ReportsInput) {
  const [selectedReportId, setSelectedReportId] = useState<string | null>(null);
  const reports = useQuery({
    ...researchV6DirectorReportsOptions(transport, workspaceId, runId),
    enabled,
  });
  const reportId =
    (selectedReportId &&
    reports.data?.some(
      (item) => item.id === selectedReportId && Boolean(item.packageHash),
    )
      ? selectedReportId
      : null) ??
    reports.data?.find((item) => Boolean(item.packageHash))?.id ??
    null;
  const detail = useQuery({
    ...researchV6DirectorReportOptions(
      transport,
      workspaceId,
      runId,
      reportId ?? "00000000-0000-0000-0000-000000000000",
    ),
    enabled: enabled && deliveryOpen && Boolean(reportId),
  });
  const sandbox = validateResearchV6ReportSandboxUrl(
    detail.data?.sandboxUrl ?? "",
    typeof window === "undefined" ? "" : window.location.origin,
    detail.data?.reportOrigin ?? "",
  );
  const compiled = useQuery({
    ...researchV6DirectorReportCompiledOptions(
      transport,
      workspaceId,
      runId,
      reportId ?? "00000000-0000-4000-8000-000000000000",
    ),
    enabled:
      enabled &&
      deliveryOpen &&
      Boolean(reportId) &&
      Boolean(detail.data) &&
      !sandbox.ok,
  });

  return {
    reports: reports.data,
    reportsLoading: reports.isLoading,
    refetchReports: reports.refetch,
    reportId,
    selectReport: setSelectedReportId,
    reportDetail: detail.data,
    reportDetailFetching: detail.isFetching,
    refetchReportDetail: detail.refetch,
    compiledHtml: compiled.data,
    compiledFetching: compiled.isFetching,
  };
}
