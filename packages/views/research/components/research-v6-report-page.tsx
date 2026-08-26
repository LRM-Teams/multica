"use client";

import { useMemo } from "react";
import { api } from "@multica/core/api";
import { createResearchV6DirectorProjectionTransport } from "@multica/core/api/research-v6-director";
import { useWorkspaceId } from "@multica/core/hooks";
import { useNavigation } from "../../navigation/context";
import { useResearchV6Reports } from "../v6-session-adapter";
import { ResearchV6ReportModal } from "./research-v6-report-modal";

const ACTIVE_REPORT_WORK_STATUSES = new Set([
  "ready",
  "dispatching",
  "enqueued",
  "running",
  "awaiting_input",
]);

export function ResearchV6ReportPage({ sessionId }: { sessionId: string }) {
  const workspaceId = useWorkspaceId();
  const navigation = useNavigation();
  const transport = useMemo(
    () => createResearchV6DirectorProjectionTransport(api),
    [],
  );
  const {
    reports,
    reportsLoading,
    reportId,
    selectReport,
    reportDetail,
    reportDetailFetching,
    refetchReportDetail,
    compiledHtml,
    compiledFetching,
  } = useResearchV6Reports({
    enabled: true,
    deliveryOpen: true,
    workspaceId,
    runId: sessionId,
    transport,
  });

  return (
    <main className="min-h-dvh bg-background" data-testid="research-v6-report-page">
      <ResearchV6ReportModal
        open
        presentation="page"
        onOpenChange={(open) => {
          if (!open) navigation.back();
        }}
        appOrigin={typeof window === "undefined" ? "" : window.location.origin}
        report={
          reportDetail
            ? {
                id: reportDetail.id,
                title: reportDetail.title,
                packageHash: reportDetail.packageHash,
                sandboxUrl: reportDetail.sandboxUrl ?? "",
                reportOrigin: reportDetail.reportOrigin ?? "",
                compiledHtml,
                plainTextFallback: reportDetail.plainText,
                revision: reportDetail.revision,
                status: reportDetail.status,
                maturity: reportDetail.maturity,
                directionCoverage: reportDetail.directionCoverage,
                updatedAt: reportDetail.updatedAt,
                inputCount:
                  reports?.find((item) => item.id === reportDetail.id)
                    ?.inputCount ?? reportDetail.inputRefs.length,
              }
            : null
        }
        history={(reports ?? [])
          .filter((item) => Boolean(item.packageHash))
          .map((item) => ({
            id: item.id,
            revision: item.revision,
            status: item.status,
            title: item.title,
            publishedAt: item.publishedAt,
          }))}
        onSelectReport={selectReport}
        selectedReportId={reportId}
        loading={reportsLoading || reportDetailFetching || compiledFetching}
        updating={ACTIVE_REPORT_WORK_STATUSES.has(reports?.[0]?.workStatus ?? "")}
        onRequestFreshCapability={() => void refetchReportDetail()}
      />
    </main>
  );
}
