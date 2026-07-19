import { queryOptions } from "@tanstack/react-query";
import { api } from "../api";
import type { EvolutionReviewSubmissionStatus } from "../types";

export const evolutionKeys = {
  all: (wsId: string) => ["evolution", wsId] as const,
  reviewSubmissions: (wsId: string, status: EvolutionReviewSubmissionStatus) =>
    ["evolution", wsId, "review-submissions", status] as const,
  reviewSubmission: (wsId: string, submissionId: string) =>
    ["evolution", wsId, "review-submissions", submissionId] as const,
  metrics: (wsId: string) => ["evolution", wsId, "metrics"] as const,
  memoryCurationStatus: (wsId: string) => ["evolution", wsId, "memory-curation-status"] as const,
  memoryCurationRun: (wsId: string, runId: string) => ["evolution", wsId, "memory-curation-run", runId] as const,
  memoryCuratorProfile: (wsId: string) => ["evolution", wsId, "memory-curator-profile"] as const,
};

export function evolutionReviewSubmissionListOptions(
  wsId: string,
  status: EvolutionReviewSubmissionStatus = "needs_review",
) {
  return queryOptions({
    queryKey: evolutionKeys.reviewSubmissions(wsId, status),
    queryFn: () => api.listEvolutionReviewSubmissions({ status }),
    enabled: !!wsId,
  });
}

export function evolutionMetricsOptions(wsId: string) {
  return queryOptions({
    queryKey: evolutionKeys.metrics(wsId),
    queryFn: () => api.getEvolutionMetrics(),
    enabled: !!wsId,
  });
}

const ACTIVE_MEMORY_CURATION_STATUSES = new Set(["queued", "waiting_runtime", "running"]);

export function workspaceMemoryCurationStatusOptions(wsId: string) {
  return queryOptions({
    queryKey: evolutionKeys.memoryCurationStatus(wsId),
    queryFn: () => api.getWorkspaceMemoryCurationStatus(wsId),
    enabled: !!wsId,
    refetchInterval: (query) => (
      query.state.data?.stages?.some((stage) => ACTIVE_MEMORY_CURATION_STATUSES.has(stage.status)) ? 5000 : false
    ),
  });
}

export function memoryCurationRunOptions(wsId: string, runId: string) {
  return queryOptions({
    queryKey: evolutionKeys.memoryCurationRun(wsId, runId),
    queryFn: () => api.getMemoryCurationRun(wsId, runId),
    enabled: !!wsId && !!runId,
    refetchInterval: (query) => (
      ACTIVE_MEMORY_CURATION_STATUSES.has(query.state.data?.status ?? "") ? 5000 : false
    ),
  });
}

export function memoryCuratorProfileOptions(wsId: string) {
  return queryOptions({
    queryKey: evolutionKeys.memoryCuratorProfile(wsId),
    queryFn: () => api.getMemoryCuratorProfile(wsId),
    enabled: !!wsId,
  });
}

export function evolutionReviewSubmissionDetailOptions(wsId: string, submissionId: string) {
  return queryOptions({
    queryKey: evolutionKeys.reviewSubmission(wsId, submissionId),
    queryFn: () => api.getEvolutionReviewSubmission(submissionId),
    enabled: !!wsId && !!submissionId,
  });
}
