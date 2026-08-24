import type { ApiClient } from "./client";
import type {
  ResearchV6DirectorDetailTransport,
  ResearchV6DirectorProjectionTransport,
} from "../types/research-v6-director";

/** Real HTTP transport for the authoritative Director V6 projection contract. */
export function createResearchV6DirectorProjectionTransport(
  api: ApiClient,
): ResearchV6DirectorProjectionTransport & ResearchV6DirectorDetailTransport {
  return {
    loadSnapshot: (workspaceId, runId, cursor, signal) =>
      api.getResearchV6DirectorProjectionSnapshot(workspaceId, runId, {
        cursor,
        signal,
      }),
    loadSlice: (workspaceId, runId, request, signal) =>
      api.getResearchV6DirectorProjectionSlice(workspaceId, runId, request, {
        signal,
      }),
    loadDeltaPage: (workspaceId, runId, after, cursor, signal) =>
      api.getResearchV6DirectorProjectionDeltaPage(workspaceId, runId, after, {
        cursor,
        signal,
      }),
    resume: (workspaceId, runId, request, signal) =>
      api.resumeResearchV6DirectorProjection(workspaceId, runId, request, {
        signal,
      }),
    loadNodeDetail: (workspaceId, runId, snapshotId, nodeId, view, signal) =>
      api.getResearchV6DirectorProjectionNodeDetail(
        workspaceId,
        runId,
        snapshotId,
        nodeId,
        view,
        { signal },
      ),
    loadWorkActivity: (workspaceId, runId, workItemId, signal) =>
      api.getResearchV6DirectorWorkActivity(workspaceId, runId, workItemId, {
        signal,
      }),
    listReports: (workspaceId, runId, signal) =>
      api.getResearchV6DirectorReports(workspaceId, runId, { signal }),
    loadReport: (workspaceId, runId, reportId, signal) =>
      api.getResearchV6DirectorReport(workspaceId, runId, reportId, { signal }),
    loadCompiledReport: (workspaceId, runId, reportId, signal) =>
      api.getResearchV6DirectorReportCompiled(workspaceId, runId, reportId, {
        signal,
      }),
  };
}
