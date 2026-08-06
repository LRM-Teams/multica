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
  trainingExamples: (wsId: string, modelKind = "", status = "", split = "") => ["evolution", wsId, "training-examples", modelKind, status, split] as const,
  modelConfigs: (wsId: string) => ["evolution", wsId, "model-configs"] as const,
  modelEvalRuns: (wsId: string, modelKind = "") => ["evolution", wsId, "model-eval-runs", modelKind] as const,
  memoryCurationStatus: (wsId: string) => ["evolution", wsId, "memory-curation-status"] as const,
  memoryCurationRun: (wsId: string, runId: string) => ["evolution", wsId, "memory-curation-run", runId] as const,
  memoryCuratorProfile: (wsId: string) => ["evolution", wsId, "memory-curator-profile"] as const,
  memoryCurationDailySummary: (wsId: string, since = "", until = "") =>
    ["evolution", wsId, "memory-curation-daily-summary", since, until] as const,
  memoryCurationCandidates: (wsId: string, date: string, kind = "all") =>
    ["evolution", wsId, "memory-curation-candidates", date, kind] as const,
  memoryCurationCandidate: (wsId: string, candidateId: string) =>
    ["evolution", wsId, "memory-curation-candidate", candidateId] as const,
  teamKnowledge: (wsId: string, date = "", kind = "") =>
    ["evolution", wsId, "team-knowledge", date, kind] as const,
  teamKnowledgeItem: (wsId: string, itemId: string) =>
    ["evolution", wsId, "team-knowledge-item", itemId] as const,
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

export function memoryCurationDailySummaryOptions(
  wsId: string,
  params: { since?: string; until?: string; timezone?: string } = {},
) {
  return queryOptions({
    queryKey: evolutionKeys.memoryCurationDailySummary(wsId, params.since ?? "", params.until ?? ""),
    queryFn: () => api.getMemoryCurationDailySummary(wsId, params),
    enabled: !!wsId,
  });
}

export function memoryCurationCandidatesOptions(
  wsId: string,
  params: { date: string; kind?: "memory" | "skill" | "all"; status?: string; timezone?: string },
) {
  return queryOptions({
    queryKey: evolutionKeys.memoryCurationCandidates(wsId, params.date, params.kind ?? "all"),
    queryFn: () => api.listMemoryCurationCandidates(wsId, params),
    enabled: !!wsId && !!params.date,
  });
}

export function memoryCurationCandidateOptions(wsId: string, candidateId: string) {
  return queryOptions({
    queryKey: evolutionKeys.memoryCurationCandidate(wsId, candidateId),
    queryFn: () => api.getMemoryCurationCandidate(wsId, candidateId),
    enabled: !!wsId && !!candidateId,
  });
}

export function teamKnowledgeListOptions(
  wsId: string,
  params: { date?: string; kind?: string; timezone?: string } = {},
) {
  return queryOptions({
    queryKey: evolutionKeys.teamKnowledge(wsId, params.date ?? "", params.kind ?? ""),
    queryFn: () => api.listTeamKnowledgeItems(wsId, params),
    enabled: !!wsId,
  });
}

export function teamKnowledgeItemOptions(wsId: string, itemId: string) {
  return queryOptions({
    queryKey: evolutionKeys.teamKnowledgeItem(wsId, itemId),
    queryFn: () => api.getTeamKnowledgeItem(wsId, itemId),
    enabled: !!wsId && !!itemId,
  });
}

export function evolutionReviewSubmissionDetailOptions(wsId: string, submissionId: string) {
  return queryOptions({
    queryKey: evolutionKeys.reviewSubmission(wsId, submissionId),
    queryFn: () => api.getEvolutionReviewSubmission(submissionId),
    enabled: !!wsId && !!submissionId,
  });
}

export function evolutionTrainingExamplesOptions(
  wsId: string,
  params: { model_kind?: string; status?: string; split?: string; limit?: number } = {},
) {
  return queryOptions({
    queryKey: evolutionKeys.trainingExamples(wsId, params.model_kind ?? "", params.status ?? "", params.split ?? ""),
    queryFn: () => api.listEvolutionTrainingExamples(params),
    enabled: !!wsId,
  });
}

export function evolutionModelConfigsOptions(wsId: string) {
  return queryOptions({
    queryKey: evolutionKeys.modelConfigs(wsId),
    queryFn: () => api.listEvolutionModelConfigs(),
    enabled: !!wsId,
  });
}

export function evolutionModelEvalRunsOptions(wsId: string, params: { model_kind?: string; limit?: number } = {}) {
  return queryOptions({
    queryKey: evolutionKeys.modelEvalRuns(wsId, params.model_kind ?? ""),
    queryFn: () => api.listEvolutionModelEvalRuns(params),
    enabled: !!wsId,
  });
}
