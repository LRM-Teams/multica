"use client";

import { useMemo, useState, type ReactNode } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import { showErrorToast } from "@multica/ui/lib/error-toast";
import {
  Bot,
  CheckCircle2,
  ExternalLink,
  FileText,
  PackageCheck,
  RefreshCw,
  RotateCcw,
  ShieldAlert,
  XCircle,
} from "lucide-react";
import { Badge } from "@multica/ui/components/ui/badge";
import { Button } from "@multica/ui/components/ui/button";
import { Checkbox } from "@multica/ui/components/ui/checkbox";
import { Card, CardContent } from "@multica/ui/components/ui/card";
import { Label } from "@multica/ui/components/ui/label";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@multica/ui/components/ui/select";
import { Textarea } from "@multica/ui/components/ui/textarea";
import { api } from "@multica/core/api";
import { useAuthStore } from "@multica/core/auth";
import { useWorkspaceId } from "@multica/core/hooks";
import { paths, useCurrentWorkspace } from "@multica/core/paths";
import { agentListOptions, memberListOptions, workspaceKeys } from "@multica/core/workspace/queries";
import {
  evolutionKeys,
  evolutionMetricsOptions,
  evolutionReviewSubmissionDetailOptions,
  evolutionReviewSubmissionListOptions,
} from "@multica/core/evolution";
import type {
  EvolutionReviewSubmission,
  EvolutionReviewSubmissionStatus,
} from "@multica/core/types";
import { useT } from "../../i18n";
import { AppLink } from "../../navigation";

const REVIEW_STATUSES: EvolutionReviewSubmissionStatus[] = [
  "needs_review",
  "rejected",
  "promoted",
  "candidate",
];

export function EvolutionReviewSection() {
  const { t } = useT("skills");
  const wsId = useWorkspaceId();
  const workspace = useCurrentWorkspace();
  const qc = useQueryClient();
  const [status, setStatus] = useState<EvolutionReviewSubmissionStatus>("needs_review");
  const [selectedId, setSelectedId] = useState<string | null>(null);
  const [reason, setReason] = useState("");
  const [applyReviewSuggestions, setApplyReviewSuggestions] = useState(false);
  const statusLabels = {
    needs_review: t(($) => $.evolution_review.statuses.needs_review),
    rejected: t(($) => $.evolution_review.statuses.rejected),
    promoted: t(($) => $.evolution_review.statuses.promoted),
    candidate: t(($) => $.evolution_review.statuses.candidate),
  };

  const query = useQuery(evolutionReviewSubmissionListOptions(wsId, status));
  const { data: agents = [] } = useQuery(agentListOptions(wsId));
  const { data: members = [] } = useQuery(memberListOptions(wsId));
  const { data: metrics } = useQuery(evolutionMetricsOptions(wsId));
  const submissions = useMemo(() => query.data ?? [], [query.data]);
  const selectedSummary = useMemo(
    () => submissions.find((item) => item.id === selectedId) ?? submissions[0] ?? null,
    [selectedId, submissions],
  );
  const detailQuery = useQuery(
    evolutionReviewSubmissionDetailOptions(wsId, selectedSummary?.id ?? ""),
  );
  const selected = detailQuery.data ?? selectedSummary;
  const sourceAgent = useMemo(
    () => agents.find((agent) => agent.id === selected?.source_agent_id) ?? null,
    [agents, selected?.source_agent_id],
  );
  const workspaceSlug = workspace?.slug ?? "";

  const invalidateReviewQueue = async () => {
    await qc.invalidateQueries({ queryKey: evolutionKeys.all(wsId) });
  };

  const promote = useMutation({
    mutationFn: (submissionId: string) =>
      api.promoteEvolutionReviewSubmission(submissionId, {
        reason,
        apply_review_suggestions: applyReviewSuggestions,
      }),
    onSuccess: async () => {
      toast.success(t(($) => $.evolution_review.toast_promoted));
      setReason("");
      setApplyReviewSuggestions(false);
      await invalidateReviewQueue();
    },
    onError: (e) => showErrorToast(e instanceof Error ? e.message : t(($) => $.evolution_review.toast_failed)),
  });

  const toggleSkill = useMutation({
    mutationFn: async ({ submissionId, enabled }: { submissionId: string; enabled: boolean }) => {
      if (!canManageReview) throw new Error(t(($) => $.evolution_review.insufficient_permissions));
      await api.setEvolutionSourceSkillAssignment(submissionId, enabled);
    },
    onSuccess: async () => {
      toast.success(t(($) => $.evolution_review.skill_assignment_updated));
      await qc.invalidateQueries({ queryKey: workspaceKeys.agents(wsId) });
    },
    onError: (e) => showErrorToast(e instanceof Error ? e.message : t(($) => $.evolution_review.toast_failed)),
  });

  const reject = useMutation({
    mutationFn: (submissionId: string) =>
      api.rejectEvolutionReviewSubmission(submissionId, { reason }),
    onSuccess: async () => {
      toast.success(t(($) => $.evolution_review.toast_rejected));
      setReason("");
      await invalidateReviewQueue();
    },
    onError: (e) => showErrorToast(e instanceof Error ? e.message : t(($) => $.evolution_review.toast_failed)),
  });

  const currentUserId = useAuthStore((state) => state.user?.id ?? null);
  const currentRole = members.find((member) => member.user_id === currentUserId)?.role;
  const canManageReview = currentRole === "owner" || currentRole === "admin";
  const canDecide = canManageReview && selected?.status === "needs_review";
  const deciding = promote.isPending || reject.isPending;

  return (
    <div className="space-y-6">
      <section>
        <p className="text-sm text-muted-foreground">
          {t(($) => $.page.review_queue.description)}
        </p>
      </section>

      <div className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
        <Select
          value={status}
          onValueChange={(value) => {
            setStatus(value as EvolutionReviewSubmissionStatus);
            setSelectedId(null);
          }}
        >
          <SelectTrigger className="w-full sm:w-48" aria-label={t(($) => $.evolution_review.status_filter)}>
            <SelectValue>{statusLabel(statusLabels, status)}</SelectValue>
          </SelectTrigger>
          <SelectContent>
            {REVIEW_STATUSES.map((item) => (
              <SelectItem key={item} value={item}>
                {statusLabel(statusLabels, item)}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
        <Button variant="outline" size="sm" onClick={() => query.refetch()} disabled={query.isFetching}>
          <RefreshCw className="h-4 w-4" />
          {query.isFetching ? t(($) => $.evolution_review.refreshing) : t(($) => $.evolution_review.refresh)}
        </Button>
      </div>

      {query.isLoading ? (
        <ReviewQueueState icon={<RefreshCw className="h-4 w-4 animate-spin" />} title={t(($) => $.evolution_review.loading)} />
      ) : submissions.length === 0 ? (
        <ReviewQueueState icon={<CheckCircle2 className="h-4 w-4" />} title={t(($) => $.evolution_review.empty_title)} description={t(($) => $.evolution_review.empty_description)} />
      ) : (
        <div className="grid gap-4 lg:grid-cols-[minmax(0,0.95fr)_minmax(0,1.25fr)]">
          <div className="space-y-2">
            {submissions.map((submission) => (
              <SubmissionListItem
                key={submission.id}
                submission={submission}
                sourceAgentName={agents.find((agent) => agent.id === submission.source_agent_id)?.name}
                active={submission.id === selected?.id}
                onSelect={() => setSelectedId(submission.id)}
              />
            ))}
          </div>

          {selected && (
            <Card className="lg:sticky lg:top-4 lg:self-start">
              <CardContent className="space-y-5">
                <div className="space-y-2">
                  <div className="flex flex-wrap items-center gap-2">
                    <Badge variant="outline">{unitTypeLabel(t, selected.unit_type)}</Badge>
                    <RiskBadge risk={selected.review_risk_level} />
                    <Badge variant="secondary">{statusLabel(statusLabels, selected.status)}</Badge>
                  </div>
                  <div>
                    <h3 className="text-base font-semibold">{selected.title || t(($) => $.evolution_review.untitled)}</h3>
                    <p className="mt-1 text-sm text-muted-foreground">{selected.summary || t(($) => $.evolution_review.no_summary)}</p>
                  </div>
                </div>

                <dl className="grid grid-cols-2 gap-3 text-sm">
                  <ReviewFact label={t(($) => $.evolution_review.source_agent)} value={sourceAgent?.name ?? shortId(selected.source_agent_id) ?? t(($) => $.evolution_review.unknown)} />
                  <ReviewFact label={t(($) => $.evolution_review.submission_confidence)} value={confidenceLabel(t, selected.confidence)} />
                  <ReviewFact label={t(($) => $.evolution_review.sensitivity)} value={sensitivityLabel(t, selected.sensitivity)} />
                  <ReviewFact label={t(($) => $.evolution_review.decision)} value={reviewDecisionLabel(t, selected.review_decision)} />
                  <ReviewFact label={t(($) => $.evolution_review.confidence)} value={formatConfidence(selected.review_confidence)} />
                  <ReviewFact label={t(($) => $.evolution_review.updated)} value={formatDateTime(selected.updated_at)} />
                </dl>

                <ReviewAuditSummary submission={selected} />

                <CandidateEvidence submission={selected} />

                <ReuseFeedback
                  metric={metrics?.unit_metrics.find((item) =>
                    item.unit_id === selected.promoted_unit_id ||
                    (item.local_unit_id === selected.local_unit_id && item.unit_type === selected.unit_type),
                  )}
                />

                <DeliveryPreview
                  submission={selected}
                  sourceAgent={sourceAgent}
                  workspaceSlug={workspaceSlug}
                  assignmentPending={toggleSkill.isPending || !canManageReview}
                  onToggleSkill={(enabled) => {
                    if (sourceAgent && selected.materialized_skill) {
                      toggleSkill.mutate({ submissionId: selected.id, enabled });
                    }
                  }}
                />

                <section className="space-y-2">
                  <h4 className="text-sm font-medium">{t(($) => $.evolution_review.review_reason)}</h4>
                  <p className="rounded-lg border bg-muted/30 p-3 text-sm text-muted-foreground whitespace-pre-wrap">
                    {selected.review_reason || selected.reject_reason || reviewString(selected.review_metadata, "rationale") || t(($) => $.evolution_review.no_review_reason)}
                  </p>
                </section>

                <ReviewMetadata metadata={selected.review_metadata} />

                <section className="space-y-2">
                  <h4 className="text-sm font-medium">{t(($) => $.evolution_review.content)}</h4>
                  <pre className="max-h-56 overflow-auto rounded-lg border bg-muted/30 p-3 text-xs whitespace-pre-wrap">
                    {selected.content || t(($) => $.evolution_review.no_content)}
                  </pre>
                </section>

                <ReviewFiles submission={selected} />

                <section className="space-y-2">
                  <Label htmlFor="evolution-review-reason">{t(($) => $.evolution_review.reason_label)}</Label>
                  <Textarea
                    id="evolution-review-reason"
                    value={reason}
                    onChange={(event) => setReason(event.target.value)}
                    placeholder={t(($) => $.evolution_review.reason_placeholder)}
                    disabled={!canDecide || deciding}
                  />
                  <label className="flex items-start gap-2 rounded-lg border bg-muted/20 p-3 text-sm">
                    <Checkbox
                      checked={applyReviewSuggestions}
                      onCheckedChange={(checked) => setApplyReviewSuggestions(checked === true)}
                      disabled={!canDecide || deciding || !hasReviewSuggestions(selected.review_metadata)}
                      className="mt-0.5"
                    />
                    <span className="space-y-1">
                      <span className="block font-medium">{t(($) => $.evolution_review.apply_suggestions)}</span>
                      <span className="block text-xs text-muted-foreground">
                        {hasReviewSuggestions(selected.review_metadata)
                          ? t(($) => $.evolution_review.apply_suggestions_hint)
                          : t(($) => $.evolution_review.no_suggestions_hint)}
                      </span>
                    </span>
                  </label>
                  <div className="flex flex-col gap-2 sm:flex-row sm:justify-end">
                    <Button
                      variant="outline"
                      onClick={() => selected && reject.mutate(selected.id)}
                      disabled={!canDecide || deciding}
                    >
                      <XCircle className="h-4 w-4" />
                      {t(($) => $.evolution_review.reject)}
                    </Button>
                    <Button
                      onClick={() => selected && promote.mutate(selected.id)}
                      disabled={!canDecide || deciding}
                    >
                      <CheckCircle2 className="h-4 w-4" />
                      {t(($) => $.evolution_review.promote)}
                    </Button>
                  </div>
                  {!canDecide && (
                    <p className="text-xs text-muted-foreground">
                      {t(($) => $.evolution_review.decision_disabled_hint)}
                    </p>
                  )}
                </section>
              </CardContent>
            </Card>
          )}
        </div>
      )}
    </div>
  );
}

function SubmissionListItem({
  submission,
  sourceAgentName,
  active,
  onSelect,
}: {
  submission: EvolutionReviewSubmission;
  sourceAgentName?: string;
  active: boolean;
  onSelect: () => void;
}) {
  const { t } = useT("skills");
  return (
    <button
      type="button"
      onClick={onSelect}
      className={`w-full rounded-xl border p-4 text-left transition hover:bg-muted/40 ${
        active ? "border-primary bg-muted/50" : "bg-card"
      }`}
    >
      <div className="flex items-start justify-between gap-3">
        <div className="min-w-0 space-y-1">
          <p className="truncate text-sm font-medium">{submission.title || t(($) => $.evolution_review.untitled)}</p>
          <p className="line-clamp-2 text-xs text-muted-foreground">
            {submission.review_reason || submission.summary || t(($) => $.evolution_review.no_summary)}
          </p>
        </div>
        <RiskBadge risk={submission.review_risk_level} />
      </div>
      <div className="mt-3 flex flex-wrap items-center gap-2 text-xs text-muted-foreground">
        <span>{sourceAgentName ?? shortId(submission.source_agent_id) ?? t(($) => $.evolution_review.unknown)}</span>
        <span>-</span>
        <span>{unitTypeLabel(t, submission.unit_type)}</span>
        <span>-</span>
        <span>{confidenceLabel(t, submission.confidence)}</span>
        <span>-</span>
        <span>{formatDateTime(submission.updated_at)}</span>
      </div>
    </button>
  );
}

function RiskBadge({ risk }: { risk: string }) {
  const { t } = useT("skills");
  const normalized = risk === "low" || risk === "medium" || risk === "high" ? risk : "unknown";
  const className =
    normalized === "high"
      ? "border-destructive/30 bg-destructive/10 text-destructive"
      : normalized === "medium"
        ? "border-amber-300 bg-amber-50 text-amber-700"
        : "";
  return (
    <Badge variant={normalized === "low" ? "secondary" : "outline"} className={className}>
      <ShieldAlert className="h-3 w-3" />
      {t(($) => $.evolution_review.risks[normalized])}
    </Badge>
  );
}

function ReviewFact({ label, value }: { label: string; value: string }) {
  return (
    <div className="rounded-lg border bg-muted/20 p-3">
      <dt className="text-xs text-muted-foreground">{label}</dt>
      <dd className="mt-1 truncate font-medium" title={value}>{value}</dd>
    </div>
  );
}

function ReviewAuditSummary({ submission }: { submission: EvolutionReviewSubmission }) {
  const { t } = useT("skills");
  const metadata = submission.review_metadata;
  const allItems: Array<[string, string | null]> = [
    [t(($) => $.evolution_review.metadata_source), metadataSourceLabel(t, reviewString(metadata, "source"))],
    [
      t(($) => $.evolution_review.metadata_provider),
      nestedReviewString(metadata, ["metadata", "provider"]) ?? reviewString(metadata, "provider"),
    ],
    [
      t(($) => $.evolution_review.metadata_model),
      nestedReviewString(metadata, ["metadata", "model"]) ?? reviewString(metadata, "model"),
    ],
    [
      t(($) => $.evolution_review.metadata_session),
      nestedReviewString(metadata, ["metadata", "session_id"]) ?? reviewString(metadata, "session_id"),
    ],
    [
      t(($) => $.evolution_review.metadata_prompt_version),
      nestedReviewString(metadata, ["metadata", "prompt_version"]) ?? reviewString(metadata, "prompt_version"),
    ],
    [t(($) => $.evolution_review.metadata_rationale), reviewString(metadata, "rationale")],
    [t(($) => $.evolution_review.metadata_risks), reviewStringList(metadata["risks"])],
  ];
  const items = allItems.filter((item): item is [string, string] => Boolean(item[1]));

  if (items.length === 0) return null;
  return (
    <section className="space-y-2 rounded-lg border bg-muted/20 p-3">
      <h4 className="text-sm font-medium">{t(($) => $.evolution_review.audit_summary)}</h4>
      <dl className="space-y-2 text-sm">
        {items.map(([label, value]) => (
          <div key={label} className="grid gap-1 sm:grid-cols-[7rem_minmax(0,1fr)]">
            <dt className="text-xs text-muted-foreground">{label}</dt>
            <dd className="min-w-0 break-words text-xs font-medium">{value}</dd>
          </div>
        ))}
      </dl>
    </section>
  );
}

function CandidateEvidence({ submission }: { submission: EvolutionReviewSubmission }) {
  const { t } = useT("skills");
  const evidenceEntries = Object.entries(submission.evidence ?? {});
  const draft = submission.unit_type === "skill" ? submission.files?.find((file) => file.path === "SKILL.md") : null;
  if (evidenceEntries.length === 0 && !draft) return null;

  return (
    <section className="space-y-3 rounded-lg border bg-muted/20 p-3">
      <h4 className="text-sm font-medium">{t(($) => $.evolution_review.candidate_evidence)}</h4>
      {evidenceEntries.length > 0 && (
        <dl className="space-y-2 text-xs">
          {evidenceEntries.map(([key, value]) => (
            <div key={key} className="grid gap-1 sm:grid-cols-[8rem_minmax(0,1fr)]">
              <dt className="text-muted-foreground">{evidenceLabel(t, key)}</dt>
              <dd className="break-words font-medium">{formatEvidenceValue(value)}</dd>
            </div>
          ))}
        </dl>
      )}
      {draft && (
        <details>
          <summary className="cursor-pointer text-sm font-medium">{t(($) => $.evolution_review.skill_draft)}</summary>
          <pre className="mt-3 max-h-64 overflow-auto rounded-md bg-background p-3 text-xs whitespace-pre-wrap">
            {draft.content || t(($) => $.evolution_review.no_content)}
          </pre>
        </details>
      )}
    </section>
  );
}

function ReuseFeedback({
  metric,
}: {
  metric: { used_count: number; success_count: number; failure_count: number; ignored_count: number; conflict_count: number; success_rate: number } | undefined;
}) {
  const { t } = useT("skills");
  if (!metric) return null;
  return (
    <section className="space-y-2 rounded-lg border bg-muted/20 p-3">
      <h4 className="text-sm font-medium">{t(($) => $.evolution_review.reuse_feedback)}</h4>
      <dl className="grid grid-cols-2 gap-2 text-sm sm:grid-cols-3">
        <ReviewFact label={t(($) => $.evolution_review.reuse_used)} value={String(metric.used_count)} />
        <ReviewFact label={t(($) => $.evolution_review.reuse_success_rate)} value={formatConfidence(metric.success_rate)} />
        <ReviewFact label={t(($) => $.evolution_review.reuse_success)} value={String(metric.success_count)} />
        <ReviewFact label={t(($) => $.evolution_review.reuse_failure)} value={String(metric.failure_count)} />
        <ReviewFact label={t(($) => $.evolution_review.reuse_ignored)} value={String(metric.ignored_count)} />
        <ReviewFact label={t(($) => $.evolution_review.reuse_conflicts)} value={String(metric.conflict_count)} />
      </dl>
    </section>
  );
}

function DeliveryPreview({
  submission,
  sourceAgent,
  workspaceSlug,
  assignmentPending,
  onToggleSkill,
}: {
  submission: EvolutionReviewSubmission;
  sourceAgent: { id: string; name: string; skills: Array<{ id: string }> } | null;
  workspaceSlug: string;
  assignmentPending: boolean;
  onToggleSkill: (enabled: boolean) => void;
}) {
  const { t } = useT("skills");
  const outcomeType =
    submission.unit_type === "skill"
      ? t(($) => $.evolution_review.promotion_outcomes.skill)
      : submission.unit_type === "memory"
        ? t(($) => $.evolution_review.promotion_outcomes.memory)
        : t(($) => $.evolution_review.unknown);
  const promoted = Boolean(submission.promoted_unit_id);
  const agentLabel = sourceAgent?.name ?? shortId(submission.source_agent_id) ?? t(($) => $.evolution_review.unknown);
  const materializedSkill = submission.materialized_skill;
  const skillEnabled = Boolean(materializedSkill && sourceAgent?.skills.some((skill) => skill.id === materializedSkill.id));
  const agentHref = workspaceSlug && submission.source_agent_id
    ? `${paths.workspace(workspaceSlug).agentDetail(submission.source_agent_id)}?tab=${submission.unit_type === "skill" ? "skills" : "memory"}`
    : "";

  return (
    <section className="space-y-2 rounded-lg border bg-muted/20 p-3">
      <div className="flex items-center justify-between gap-3">
        <h4 className="text-sm font-medium">{t(($) => $.evolution_review.promotion_preview)}</h4>
        <Badge variant={promoted ? "secondary" : "outline"}>{promoted ? t(($) => $.evolution_review.promoted_result) : t(($) => $.evolution_review.pending_result)}</Badge>
      </div>
      <div className="grid gap-2 text-sm sm:grid-cols-2">
        <ReviewFact label={t(($) => $.evolution_review.promotion_outcome)} value={outcomeType} />
        <ReviewFact label={t(($) => $.evolution_review.promoted_unit)} value={shortId(submission.promoted_unit_id) ?? t(($) => $.evolution_review.none)} />
      </div>
      <div className="flex flex-wrap items-center gap-2 text-sm text-muted-foreground">
        <Bot className="h-4 w-4" />
        <span>{t(($) => $.evolution_review.source_agent_label)}:</span>
        {agentHref ? (
          <AppLink href={agentHref} className="inline-flex items-center gap-1 font-medium text-foreground underline-offset-4 hover:underline">
            {agentLabel}
            <ExternalLink className="h-3 w-3" />
          </AppLink>
        ) : (
          <span className="font-medium text-foreground">{agentLabel}</span>
        )}
      </div>
      {materializedSkill && (
        <div className="flex flex-col gap-2 rounded-lg border bg-background p-3 sm:flex-row sm:items-center sm:justify-between">
          <div className="min-w-0">
            <p className="truncate text-sm font-medium">{materializedSkill.name}</p>
            <p className="truncate text-xs text-muted-foreground">{materializedSkill.description}</p>
          </div>
          <Button
            variant={skillEnabled ? "outline" : "default"}
            size="sm"
            disabled={!sourceAgent || assignmentPending}
            onClick={() => onToggleSkill(!skillEnabled)}
          >
            {skillEnabled ? <RotateCcw className="h-4 w-4" /> : <PackageCheck className="h-4 w-4" />}
            {skillEnabled ? t(($) => $.evolution_review.disable_for_source) : t(($) => $.evolution_review.enable_for_source)}
          </Button>
        </div>
      )}
      {promoted && (
        <p className="inline-flex items-center gap-2 text-xs text-muted-foreground">
          <PackageCheck className="h-3.5 w-3.5" />
          {submission.unit_type === "skill"
            ? t(($) => $.evolution_review.promotion_result_hint_skill)
            : t(($) => $.evolution_review.promotion_result_hint_memory)}
        </p>
      )}
    </section>
  );
}

function ReviewMetadata({ metadata }: { metadata: Record<string, unknown> }) {
  const { t } = useT("skills");
  if (!metadata || Object.keys(metadata).length === 0) return null;
  return (
    <details className="rounded-lg border bg-muted/20 p-3">
      <summary className="cursor-pointer text-sm font-medium">{t(($) => $.evolution_review.review_metadata)}</summary>
      <pre className="mt-3 max-h-56 overflow-auto rounded-md bg-background p-3 text-xs whitespace-pre-wrap">
        {formatMetadata(metadata)}
      </pre>
    </details>
  );
}

function ReviewFiles({ submission }: { submission: EvolutionReviewSubmission }) {
  const { t } = useT("skills");
  if (!submission.files?.length) return null;
  return (
    <section className="space-y-2">
      <h4 className="text-sm font-medium">{t(($) => $.evolution_review.files)}</h4>
      <div className="space-y-2">
        {submission.files.map((file) => (
          <details key={file.id || file.path} className="rounded-lg border bg-muted/20 p-3">
            <summary className="flex cursor-pointer items-center gap-2 text-sm font-medium">
              <FileText className="h-4 w-4" />
              {file.path}
              <span className="ml-auto text-xs font-normal text-muted-foreground">
                {file.mime_type || t(($) => $.evolution_review.unknown)}
              </span>
            </summary>
            <pre className="mt-3 max-h-48 overflow-auto rounded-md bg-background p-3 text-xs whitespace-pre-wrap">
              {file.content || t(($) => $.evolution_review.no_content)}
            </pre>
          </details>
        ))}
      </div>
    </section>
  );
}

function ReviewQueueState({
  icon,
  title,
  description,
}: {
  icon: ReactNode;
  title: string;
  description?: string;
}) {
  return (
    <Card>
      <CardContent className="flex flex-col items-center justify-center gap-2 py-12 text-center">
        <div className="rounded-full border bg-muted/40 p-3 text-muted-foreground">{icon}</div>
        <p className="text-sm font-medium">{title}</p>
        {description && <p className="max-w-md text-sm text-muted-foreground">{description}</p>}
      </CardContent>
    </Card>
  );
}

type SkillsT = ReturnType<typeof useT<"skills">>["t"];

function statusLabel(labels: Record<string, string>, status: string): string {
  return labels[status] ?? status;
}

function unitTypeLabel(t: SkillsT, value: string | null | undefined): string {
  switch (value) {
    case "memory":
      return t(($) => $.evolution_review.unit_types.memory);
    case "skill":
      return t(($) => $.evolution_review.unit_types.skill);
    case "workflow":
      return t(($) => $.evolution_review.unit_types.workflow);
    case "tool_pattern":
      return t(($) => $.evolution_review.unit_types.tool_pattern);
    case "preference":
      return t(($) => $.evolution_review.unit_types.preference);
    default:
      return t(($) => $.evolution_review.unknown);
  }
}

function reviewDecisionLabel(t: SkillsT, value: string | null | undefined): string {
  switch (value) {
    case "promote":
      return t(($) => $.evolution_review.review_decisions.promote);
    case "needs_review":
      return t(($) => $.evolution_review.review_decisions.needs_review);
    case "reject":
      return t(($) => $.evolution_review.review_decisions.reject);
    default:
      return t(($) => $.evolution_review.none);
  }
}

function confidenceLabel(t: SkillsT, value: string | null | undefined): string {
  switch (value) {
    case "low":
      return t(($) => $.evolution_review.confidence_levels.low);
    case "medium":
      return t(($) => $.evolution_review.confidence_levels.medium);
    case "high":
      return t(($) => $.evolution_review.confidence_levels.high);
    default:
      return t(($) => $.evolution_review.none);
  }
}

function sensitivityLabel(t: SkillsT, value: string | null | undefined): string {
  switch (value) {
    case "none":
      return t(($) => $.evolution_review.sensitivity_levels.none);
    case "local_path":
      return t(($) => $.evolution_review.sensitivity_levels.local_path);
    case "personal":
      return t(($) => $.evolution_review.sensitivity_levels.personal);
    case "secret":
      return t(($) => $.evolution_review.sensitivity_levels.secret);
    case "unknown":
      return t(($) => $.evolution_review.sensitivity_levels.unknown);
    default:
      return t(($) => $.evolution_review.none);
  }
}

function metadataSourceLabel(t: SkillsT, value: string | null): string | null {
  switch (value) {
    case "manual_seed":
      return t(($) => $.evolution_review.metadata_sources.manual_seed);
    case "human_review":
      return t(($) => $.evolution_review.metadata_sources.human_review);
    case "deterministic_rules":
      return t(($) => $.evolution_review.metadata_sources.deterministic_rules);
    case "evolution_reviewer":
      return t(($) => $.evolution_review.metadata_sources.evolution_reviewer);
    case "agent_reviewer":
      return t(($) => $.evolution_review.metadata_sources.agent_reviewer);
    case "llm_reviewer":
      return t(($) => $.evolution_review.metadata_sources.llm_reviewer);
    default:
      return value;
  }
}

function formatConfidence(value: number | null | undefined): string {
  if (typeof value !== "number" || !Number.isFinite(value)) return "-";
  return `${Math.round(value * 100)}%`;
}

function shortId(value: string | null | undefined): string | null {
  if (!value) return null;
  return value.length > 8 ? value.slice(0, 8) : value;
}

function reviewString(metadata: Record<string, unknown>, key: string): string | null {
  const value = metadata[key];
  return typeof value === "string" && value.trim() ? value : null;
}

function nestedReviewString(metadata: Record<string, unknown>, path: string[]): string | null {
  let value: unknown = metadata;
  for (const segment of path) {
    if (!value || typeof value !== "object" || Array.isArray(value)) return null;
    value = (value as Record<string, unknown>)[segment];
  }
  return typeof value === "string" && value.trim() ? value : null;
}

function reviewStringList(value: unknown): string | null {
  if (!Array.isArray(value)) return null;
  const items = value.filter(
    (item): item is string => typeof item === "string" && item.trim().length > 0,
  );
  return items.length > 0 ? items.join(", ") : null;
}

function evidenceLabel(t: SkillsT, key: string): string {
  switch (key) {
    case "source":
      return t(($) => $.evolution_review.evidence_source);
    case "source_date":
      return t(($) => $.evolution_review.evidence_source_date);
    case "evidence_refs":
      return t(($) => $.evolution_review.evidence_refs);
    default:
      return t(($) => $.evolution_review.unknown);
  }
}

function formatEvidenceValue(value: unknown): string {
  if (typeof value === "string") return value;
  if (typeof value === "number" || typeof value === "boolean") return String(value);
  try {
    return JSON.stringify(value);
  } catch {
    return String(value);
  }
}

function formatMetadata(metadata: Record<string, unknown>): string {
  try {
    return JSON.stringify(metadata, null, 2);
  } catch {
    return String(metadata);
  }
}

function hasReviewSuggestions(metadata: Record<string, unknown>): boolean {
  return Boolean(
    reviewString(metadata, "title") ||
      reviewString(metadata, "summary") ||
      reviewString(metadata, "suggested_scope") ||
      reviewStringList(metadata["suggested_tags"]) ||
      reviewStringList(metadata["suggested_task_types"]),
  );
}

function formatDateTime(value: string | null | undefined): string {
  if (!value) return "-";
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return "-";
  return date.toLocaleString();
}
