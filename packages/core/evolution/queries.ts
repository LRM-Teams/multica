import { queryOptions } from "@tanstack/react-query";
import { api } from "../api";
import type { EvolutionReviewSubmissionStatus } from "../types";

export const evolutionKeys = {
  all: (wsId: string) => ["evolution", wsId] as const,
  reviewSubmissions: (wsId: string, status: EvolutionReviewSubmissionStatus) =>
    ["evolution", wsId, "review-submissions", status] as const,
  reviewSubmission: (wsId: string, submissionId: string) =>
    ["evolution", wsId, "review-submissions", submissionId] as const,
  metrics: (wsId: string, days = 30) => ["evolution", wsId, "metrics", days] as const,
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

export function evolutionMetricsOptions(wsId: string, days = 30) {
  return queryOptions({
    queryKey: evolutionKeys.metrics(wsId, days),
    queryFn: () => api.getEvolutionMetrics({ days }),
    enabled: !!wsId,
  });
}

export function workspaceMemoryCurationStatusOptions(wsId: string) {
  return queryOptions({
    queryKey: evolutionKeys.memoryCurationStatus(wsId),
    queryFn: () => api.getWorkspaceMemoryCurationStatus(wsId),
    enabled: !!wsId,
  });
}

export function memoryCurationRunOptions(wsId: string, runId: string) {
  return queryOptions({
    queryKey: evolutionKeys.memoryCurationRun(wsId, runId),
    queryFn: () => api.getMemoryCurationRun(wsId, runId),
    enabled: !!wsId && !!runId,
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
