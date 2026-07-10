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

export function workspaceMemoryCurationStatusOptions(wsId: string) {
  return queryOptions({
    queryKey: evolutionKeys.memoryCurationStatus(wsId),
    queryFn: () => api.getWorkspaceMemoryCurationStatus(wsId),
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
