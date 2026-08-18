import { infiniteQueryOptions, queryOptions } from "@tanstack/react-query";
import type {
  ResearchV6DirectorNodeDetailView,
  ResearchV6DirectorDetailTransport,
  ResearchV6DirectorProjectionSliceRequest,
  ResearchV6DirectorProjectionTransport,
} from "../../types/research-v6-director";

export const researchV6DirectorProjectionKeys = {
  all: (workspaceId: string, runId: string) =>
    ["research-v6-director-projection", workspaceId, runId] as const,
  snapshot: (workspaceId: string, runId: string) =>
    [...researchV6DirectorProjectionKeys.all(workspaceId, runId), "snapshot"] as const,
  slice: (
    workspaceId: string,
    runId: string,
    snapshotId: string,
    root: string,
  ) =>
    [
      ...researchV6DirectorProjectionKeys.all(workspaceId, runId),
      "slice",
      snapshotId,
      root,
      1,
    ] as const,
  deltas: (
    workspaceId: string,
    runId: string,
    snapshotId: string,
    after: number,
  ) =>
    [
      ...researchV6DirectorProjectionKeys.all(workspaceId, runId),
      "deltas",
      snapshotId,
      after,
    ] as const,
  nodeDetail: (
    workspaceId: string,
    runId: string,
    snapshotId: string,
    nodeId: string,
    view: ResearchV6DirectorNodeDetailView,
  ) =>
    [
      ...researchV6DirectorProjectionKeys.all(workspaceId, runId),
      "node-detail",
      snapshotId,
      nodeId,
      view,
    ] as const,
  reports: (workspaceId: string, runId: string) =>
    [
      ...researchV6DirectorProjectionKeys.all(workspaceId, runId),
      "reports",
    ] as const,
  report: (workspaceId: string, runId: string, reportId: string) =>
    [
      ...researchV6DirectorProjectionKeys.all(workspaceId, runId),
      "reports",
      reportId,
    ] as const,
};

export function researchV6DirectorSlicePageRequest(
  input: Omit<ResearchV6DirectorProjectionSliceRequest, "cursor" | "depth">,
  cursor: string | null,
): ResearchV6DirectorProjectionSliceRequest {
  return {
    ...input,
    depth: 1,
    cursor: cursor ?? undefined,
  };
}

/** Paginated default Projection; it never converts the query into an all-graph read. */
export function researchV6DirectorSnapshotOptions(
  transport: ResearchV6DirectorProjectionTransport,
  workspaceId: string,
  runId: string,
) {
  return infiniteQueryOptions({
    queryKey: researchV6DirectorProjectionKeys.snapshot(workspaceId, runId),
    initialPageParam: null as string | null,
    queryFn: ({ pageParam, signal }) =>
      transport.loadSnapshot(
        workspaceId,
        runId,
        pageParam ?? undefined,
        signal,
      ),
    getNextPageParam: (page) =>
      page.has_more ? (page.next_cursor ?? null) : null,
  });
}

/** One derivation layer, pinned to the exact snapshot identity. */
export function researchV6DirectorSliceOptions(
  transport: ResearchV6DirectorProjectionTransport,
  workspaceId: string,
  runId: string,
  input: Omit<ResearchV6DirectorProjectionSliceRequest, "cursor" | "depth">,
) {
  return infiniteQueryOptions({
    queryKey: researchV6DirectorProjectionKeys.slice(
      workspaceId,
      runId,
      input.snapshot_id,
      input.root,
    ),
    initialPageParam: null as string | null,
    queryFn: ({ pageParam, signal }) =>
      transport.loadSlice(
        workspaceId,
        runId,
        researchV6DirectorSlicePageRequest(input, pageParam),
        signal,
      ),
    getNextPageParam: (page) =>
      page.has_more ? (page.next_cursor ?? null) : null,
  });
}

/** Contiguous Delta pages starting after the last confirmed event sequence. */
export function researchV6DirectorDeltaOptions(
  transport: ResearchV6DirectorProjectionTransport,
  workspaceId: string,
  runId: string,
  snapshotId: string,
  after: number,
) {
  return infiniteQueryOptions({
    queryKey: researchV6DirectorProjectionKeys.deltas(
      workspaceId,
      runId,
      snapshotId,
      after,
    ),
    initialPageParam: null as string | null,
    queryFn: ({ pageParam, signal }) =>
      transport.loadDeltaPage(
        workspaceId,
        runId,
        after,
        pageParam ?? undefined,
        signal,
      ),
    getNextPageParam: (page) => page.next_cursor,
  });
}

export function researchV6DirectorNodeDetailOptions(
  transport: ResearchV6DirectorDetailTransport,
  workspaceId: string,
  runId: string,
  snapshotId: string,
  nodeId: string,
  view: ResearchV6DirectorNodeDetailView = "brief",
) {
  return queryOptions({
    queryKey: researchV6DirectorProjectionKeys.nodeDetail(
      workspaceId,
      runId,
      snapshotId,
      nodeId,
      view,
    ),
    queryFn: async ({ signal }) => {
      const detail = await transport.loadNodeDetail(
        workspaceId,
        runId,
        nodeId,
        view,
        signal,
      );
      if (detail && detail.snapshot_id !== snapshotId) {
        throw new Error("Director V6 node detail changed snapshot identity");
      }
      return detail;
    },
  });
}

export function researchV6DirectorReportsOptions(
  transport: ResearchV6DirectorDetailTransport,
  workspaceId: string,
  runId: string,
) {
  return queryOptions({
    queryKey: researchV6DirectorProjectionKeys.reports(workspaceId, runId),
    queryFn: ({ signal }) => transport.listReports(workspaceId, runId, signal),
  });
}

export function researchV6DirectorReportOptions(
  transport: ResearchV6DirectorDetailTransport,
  workspaceId: string,
  runId: string,
  reportId: string,
) {
  return queryOptions({
    queryKey: researchV6DirectorProjectionKeys.report(
      workspaceId,
      runId,
      reportId,
    ),
    queryFn: ({ signal }) =>
      transport.loadReport(workspaceId, runId, reportId, signal),
  });
}
