import type { ApiClient } from "./client";
import type { ResearchV6DirectorProjectionTransport } from "../types/research-v6-director";

/** Real HTTP transport for the authoritative Director V6 projection contract. */
export function createResearchV6DirectorProjectionTransport(
  api: ApiClient,
): ResearchV6DirectorProjectionTransport {
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
  };
}
