"use client";

import type {
  ResearchFleetMember,
  ResearchGraphNode,
  ResearchRunAttempt,
  ResearchRunSnapshot,
  ResearchRunTask,
  ResearchSource,
} from "@multica/core/types";
import { Badge } from "@multica/ui/components/ui/badge";
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
} from "@multica/ui/components/ui/sheet";
import { useIsMobile } from "@multica/ui/hooks/use-mobile";
import { X } from "lucide-react";
import { cn } from "@multica/ui/lib/utils";
import { useMemo } from "react";
import { Time } from "../../i18n/time";
import { useT } from "../../i18n/use-t";
import { useOverlayPanelA11y } from "../hooks/use-overlay-panel-a11y";
import { isAbandonedStatus, readAbandonReason } from "../lib/abandon-reason";
import { normalizeNodeStatusKey, visualForNodeType } from "../lib/node-visuals";
import { ResearchNodeContentFaces } from "./research-node-content-faces";

const EMPTY_SOURCES: ResearchSource[] = [];
const EMPTY_MEMBERS: ResearchFleetMember[] = [];

function payloadString(payload: unknown, key: string): string | null {
  if (!payload || typeof payload !== "object") return null;
  const value = (payload as Record<string, unknown>)[key];
  return typeof value === "string" && value.trim() ? value : null;
}

function payloadNumber(payload: unknown, key: string): number | null {
  if (!payload || typeof payload !== "object") return null;
  const value = (payload as Record<string, unknown>)[key];
  return typeof value === "number" && Number.isFinite(value) ? value : null;
}

function payloadRecord(payload: unknown): Record<string, unknown> {
  return payload && typeof payload === "object" && !Array.isArray(payload)
    ? (payload as Record<string, unknown>)
    : {};
}

function firstString(records: Record<string, unknown>[], keys: string[]): string | null {
  for (const record of records) {
    for (const key of keys) {
      const value = record[key];
      if (typeof value === "string" && value.trim()) return value.trim();
    }
  }
  return null;
}

function elapsedMinutes(start: string | undefined, end: string | undefined): number | null {
  if (!start || !end) return null;
  const startMs = Date.parse(start);
  const endMs = Date.parse(end);
  if (!Number.isFinite(startMs) || !Number.isFinite(endMs) || endMs < startMs) return null;
  return Math.max(1, Math.round((endMs - startMs) / 60_000));
}

function firstNumber(records: Record<string, unknown>[], key: string): number | null {
  for (const record of records) {
    const value = record[key];
    if (typeof value === "number" && Number.isFinite(value)) return value;
  }
  return null;
}

function actorLabel(member: ResearchFleetMember | undefined, actorID: string | null): string | null {
  if (member) return member.display_name || member.name || member.role || member.agent_id;
  if (!actorID) return null;
  return actorID.length > 16 ? `${actorID.slice(0, 8)}…${actorID.slice(-4)}` : actorID;
}

function taskMethodFor(
  kind: string | undefined,
  t: ReturnType<typeof useT<"research">>["t"],
): string | null {
  switch (kind) {
    case "plan":
      return t(($) => $.node.task_methods.plan);
    case "replan":
      return t(($) => $.node.task_methods.replan);
    case "discover":
      return t(($) => $.node.task_methods.discover);
    case "deep_read":
      return t(($) => $.node.task_methods.deep_read);
    case "verify":
      return t(($) => $.node.task_methods.verify);
    case "counter_search":
      return t(($) => $.node.task_methods.counter_search);
    case "synthesize":
      return t(($) => $.node.task_methods.synthesize);
    case "quality_gate":
      return t(($) => $.node.task_methods.quality_gate);
    case "citation_audit":
      return t(($) => $.node.task_methods.citation_audit);
    default:
      return null;
  }
}

function taskKindLabelFor(
  kind: string,
  t: ReturnType<typeof useT<"research">>["t"],
): string {
  switch (kind) {
    case "plan":
      return t(($) => $.node.task_kinds.plan);
    case "replan":
      return t(($) => $.node.task_kinds.replan);
    case "discover":
      return t(($) => $.node.task_kinds.discover);
    case "deep_read":
      return t(($) => $.node.task_kinds.deep_read);
    case "verify":
      return t(($) => $.node.task_kinds.verify);
    case "counter_search":
      return t(($) => $.node.task_kinds.counter_search);
    case "synthesize":
      return t(($) => $.node.task_kinds.synthesize);
    case "quality_gate":
      return t(($) => $.node.task_kinds.quality_gate);
    case "citation_audit":
      return t(($) => $.node.task_kinds.citation_audit);
    default:
      return kind;
  }
}

function expectedResultLabelFor(
  expected: string | undefined,
  t: ReturnType<typeof useT<"research">>["t"],
): string | null {
  switch (expected) {
    case "research_plan_v2":
    case "research_plan_v3":
    case "research_plan_v4":
      return t(($) => $.node.expected_results.plan);
    case "research_evidence_v2":
    case "research_evidence_v3":
    case "research_evidence_v4":
      return t(($) => $.node.expected_results.evidence);
    case "research_report_v2":
    case "research_report_v3":
    case "research_report_v4":
      return t(($) => $.node.expected_results.report);
    case "research_quality_evaluation_v2":
    case "research_quality_evaluation_v3":
    case "research_quality_evaluation_v4":
      return t(($) => $.node.expected_results.quality_evaluation);
    case "research_citation_audit_v2":
    case "research_citation_audit_v3":
    case "research_citation_audit_v4":
      return t(($) => $.node.expected_results.citation_audit);
    default:
      return expected || null;
  }
}

function executionStatusLabelFor(
  status: string,
  t: ReturnType<typeof useT<"research">>["t"],
): string {
  switch (status) {
    case "succeeded":
      return t(($) => $.node.status.completed);
    case "queued":
      return t(($) => $.node.status.pending);
    case "claimed":
    case "in_flight":
      return t(($) => $.node.status.running);
    case "retryable_failed":
    case "terminal_failed":
      return t(($) => $.node.status.failed);
    case "cancelled":
      return t(($) => $.node.status.abandoned);
    default:
      return t(($) => $.node.status[normalizeNodeStatusKey(status)]);
  }
}

type NodeRunContext = {
  task: ResearchRunTask | undefined;
  attempt: ResearchRunAttempt | undefined;
  actorID: string | null;
  actor: ResearchFleetMember | undefined;
  genericMethod: string | null;
  metrics: Array<{ key: string; label: string; value: number }>;
  reportCreated: boolean;
  producedSources: ResearchRunSnapshot["sources"];
  producedObservations: ResearchRunSnapshot["observations"];
  producedClaims: ResearchRunSnapshot["claims"];
  createdQuestions: ResearchRunSnapshot["questions"];
  decisionQuestion: string | null;
  phase: string | null;
  startedAt: string | undefined;
  completedAt: string | undefined;
  gateBlockers: string[];
  nextStep: "queued" | "running" | "retry" | "review_gate" | "review_result" | null;
};

function buildNodeRunContext(
  node: ResearchGraphNode,
  run: ResearchRunSnapshot | undefined,
  members: ResearchFleetMember[],
  labels: {
    sources: string;
    observations: string;
    claims: string;
    tasks: string;
    questions: string;
  },
): NodeRunContext {
  const payload = payloadRecord(node.payload);
  const details = payloadRecord(payload.details);
  const records = [details, payload];
  const taskID = firstString(records, ["task_id"]);
  const questionID = firstString(records, ["question_id"]);
  const attemptID = firstString(records, ["attempt_id"]);
  const task = taskID ? run?.tasks.find((item) => item.id === taskID) : undefined;
  const attempts = taskID ? (run?.attempts ?? []).filter((item) => item.task_id === taskID) : [];
  const attempt =
    (attemptID ? attempts.find((item) => item.id === attemptID) : undefined) ??
    attempts.at(-1);
  const actorID =
    node.actor_agent_id ||
    firstString(records, ["agent_id", "actor_agent_id"]) ||
    task?.assigned_agent_id ||
    attempt?.assigned_agent_id ||
    null;
  const actor = actorID ? members.find((item) => item.agent_id === actorID) : undefined;
  const producedSources = taskID
    ? (run?.sources ?? []).filter((item) => item.produced_by_task_id === taskID)
    : [];
  const producedObservations = taskID
    ? (run?.observations ?? []).filter((item) => item.produced_by_task_id === taskID)
    : [];
  const producedClaims = taskID
    ? (run?.claims ?? []).filter((item) => item.produced_by_task_id === taskID)
    : [];
  const createdQuestions = taskID
    ? (run?.questions ?? []).filter((item) => item.created_by_task_id === taskID)
    : [];
  const question = (questionID || task?.question_id)
    ? run?.questions.find((item) => item.id === (questionID || task?.question_id))
    : undefined;
  const gateBlockers =
    node.node_type === "stage_gate" && run?.gate.passed !== true
      ? run?.gate.findings.flatMap((finding) => {
          const message = finding.message.trim();
          return message ? [message] : [];
        }) ?? []
      : [];
  const taskStatus = task?.status.toLowerCase();
  const attemptStatus = attempt?.status.toLowerCase();
  const nextStep: NodeRunContext["nextStep"] =
    node.node_type === "stage_gate" && gateBlockers.length > 0
      ? "review_gate"
      : attemptStatus === "retryable_failed" || attemptStatus === "terminal_failed" || taskStatus === "failed"
        ? "retry"
        : attemptStatus === "claimed" || attemptStatus === "in_flight" || taskStatus === "in_flight"
          ? "running"
          : taskStatus === "queued" || taskStatus === "ready" || taskStatus === "pending"
            ? "queued"
            : taskStatus === "succeeded" || (Boolean(run) && node.node_type === "finding")
              ? "review_result"
              : null;
  const metricInputs = [
    ["sources", labels.sources, firstNumber(records, "sources_created") ?? producedSources.length],
    [
      "observations",
      labels.observations,
      firstNumber(records, "observations_created") ?? producedObservations.length,
    ],
    ["claims", labels.claims, firstNumber(records, "claims_created") ?? producedClaims.length],
    ["tasks", labels.tasks, firstNumber(records, "tasks_created") ?? 0],
    ["questions", labels.questions, firstNumber(records, "questions_created") ?? 0],
  ] as const;
  const metrics: NodeRunContext["metrics"] = [];
  for (const [key, label, value] of metricInputs) {
    if (value > 0) metrics.push({ key, label, value });
  }

  return {
    task,
    attempt,
    actorID,
    actor,
    genericMethod: firstString(records, ["method", "approach", "strategy", "plan"]),
    metrics,
    reportCreated: Boolean(firstString(records, ["report_id"])),
    producedSources,
    producedObservations,
    producedClaims,
    createdQuestions,
    decisionQuestion: question?.question?.trim() || null,
    phase: node.phase?.trim() || firstString(records, ["phase"]),
    startedAt: attempt?.started_at || task?.started_at || node.created_at,
    completedAt:
      attempt?.completed_at || attempt?.result_submitted_at || task?.completed_at || node.updated_at,
    gateBlockers,
    nextStep,
  };
}

function typeLabelFor(
  nodeType: string,
  t: ReturnType<typeof useT<"research">>["t"],
): string {
  switch (nodeType) {
    case "goal":
      return t(($) => $.node.goal);
    case "subquestion":
      return t(($) => $.node.subquestion);
    case "probe":
      return t(($) => $.node.probe);
    case "finding":
      return t(($) => $.node.finding);
    case "conflict":
      return t(($) => $.node.conflict);
    case "dead_end":
      return t(($) => $.node.dead_end);
    case "refuted":
      return t(($) => $.node.refuted);
    case "pivot":
      return t(($) => $.node.pivot);
    case "roster_change":
      return t(($) => $.node.roster_change);
    case "stage_gate":
      return t(($) => $.node.stage_gate);
    case "product_round_gate":
      return t(($) => $.node.product_round_gate);
    case "agent_activity":
      return t(($) => $.node.agent_activity);
    default:
      return nodeType;
  }
}

function DetailBody({
  node,
  sources,
  run,
  members,
  onClose,
  showClose,
}: {
  node: ResearchGraphNode;
  sources: ResearchSource[];
  run?: ResearchRunSnapshot;
  members: ResearchFleetMember[];
  onClose?: () => void;
  showClose?: boolean;
}) {
  const { t } = useT("research");
  const visual = visualForNodeType(node.node_type);
  const typeLabel = typeLabelFor(node.node_type, t);
  const statusKey = normalizeNodeStatusKey(node.status);
  const statusLabel = t(($) => $.node.status[statusKey]);

  const sourceId = payloadString(node.payload, "source_id");
  const linked = sourceId ? sources.find((s) => s.id === sourceId) : undefined;
  const weight =
    linked?.credibility_weight ?? payloadNumber(node.payload, "credibility_weight");
  const sourceClass = linked?.source_class || payloadString(node.payload, "source_class");
  const confidence =
    payloadNumber(node.payload, "confidence") ??
    payloadNumber(node.payload, "confidence_score");
  const deadEndReason =
    payloadString(node.payload, "reason") ||
    payloadString(node.payload, "dead_end_reason") ||
    (node.node_type === "dead_end" ? node.summary : null);
  const abandoned = isAbandonedStatus(node.status);
  const abandonReason = abandoned ? readAbandonReason(node) : null;
  const metricLabels = useMemo(
    () => ({
      sources: t(($) => $.node.source_count),
      observations: t(($) => $.node.observation_count),
      claims: t(($) => $.node.claim_count),
      tasks: t(($) => $.node.task_count),
      questions: t(($) => $.node.question_count),
    }),
    [t],
  );
  const runContext = useMemo(
    () => buildNodeRunContext(node, run, members, metricLabels),
    [members, metricLabels, node, run],
  );
  const method = taskMethodFor(runContext.task?.kind, t) || runContext.genericMethod;
  const executor = actorLabel(runContext.actor, runContext.actorID);
  const expectedResult = expectedResultLabelFor(runContext.task?.expected_result, t);
  const durationMinutes = elapsedMinutes(runContext.startedAt, runContext.completedAt);

  // Only explicitly associated sources (source_id / source_ids). Never fall
  // back to session-wide sources — that would attribute other nodes' evidence
  // to a node with no associations (LRM-1091 C-area audit).
  const evidenceList = sources
    .filter((s) => {
      if (linked && s.id === linked.id) return true;
      const ids = (node.payload as { source_ids?: unknown } | null)?.source_ids;
      return Array.isArray(ids) && ids.includes(s.id);
    })
    .slice(0, 12);

  return (
    <>
      <header className="relative border-b px-4 pt-4 pb-3 text-left">
        {showClose ? (
          <button
            type="button"
            onClick={onClose}
            className="absolute top-3 right-3 rounded-md p-1 text-muted-foreground hover:bg-muted hover:text-foreground"
            aria-label={t(($) => $.overlay.detail_close)}
            data-autofocus="true"
          >
            <X className="size-4" aria-hidden />
          </button>
        ) : null}
        <div className={cn("mb-1 flex flex-wrap items-center gap-2", showClose && "pr-8")}>
          <span className={`h-2 w-2 rounded-full ${visual.accentBarClass}`} />
          <Badge variant="outline" className="text-[10px] uppercase">
            {typeLabel}
          </Badge>
          <Badge variant="secondary" className="text-[10px]">
            {statusLabel}
          </Badge>
          {sourceClass ? (
            <Badge variant="outline" className="text-[10px]">
              {sourceClass}
            </Badge>
          ) : null}
          {typeof weight === "number" ? (
            <Badge variant="secondary" className="text-[10px]">
              {t(($) => $.panel.weight)} {weight.toFixed(2)}
            </Badge>
          ) : null}
          {runContext.phase ? (
            <Badge variant="outline" className="text-[10px]">
              {t(($) => $.node.phase)} {runContext.phase}
            </Badge>
          ) : null}
          {executor ? (
            <Badge variant="outline" className="text-[10px]">
              {executor}
            </Badge>
          ) : null}
          {typeof confidence === "number" ? (
            <Badge variant="secondary" className="text-[10px]">
              {t(($) => $.node.confidence)}{" "}
              {(confidence <= 1 ? confidence * 100 : confidence).toFixed(0)}%
            </Badge>
          ) : null}
        </div>
        <h2 className="text-base leading-snug font-semibold">{node.title}</h2>
        {runContext.decisionQuestion ? (
          <div className="mt-2">
            <p className="text-[11px] font-semibold tracking-wide text-muted-foreground uppercase">
              {t(($) => $.node.decision_question)}
            </p>
            <p className="mt-0.5 text-sm leading-relaxed">{runContext.decisionQuestion}</p>
          </div>
        ) : null}
        <dl className="mt-2 flex flex-wrap gap-x-4 gap-y-1 text-[11px] text-muted-foreground">
          {runContext.startedAt ? (
            <div className="flex gap-1"><dt>{t(($) => $.node.started_at)}</dt><dd><Time kind="full" value={runContext.startedAt} /></dd></div>
          ) : null}
          {node.updated_at ? (
            <div className="flex gap-1"><dt>{t(($) => $.node.updated_at)}</dt><dd><Time kind="full" value={node.updated_at} /></dd></div>
          ) : null}
          {durationMinutes ? (
            <div className="flex gap-1"><dt>{t(($) => $.node.duration)}</dt><dd>{t(($) => $.node.duration_minutes, { count: durationMinutes })}</dd></div>
          ) : null}
        </dl>
        <p className="sr-only">{t(($) => $.node.detail_hint)}</p>
      </header>

      <div className="space-y-4 p-4">
        {/* LRM-1332: four content faces before run Objective/Method/Outcome. */}
        <ResearchNodeContentFaces node={node} density="detail" />

        {runContext.task ? (
          <section className="grid gap-3 rounded-lg border bg-muted/15 p-3 sm:grid-cols-2">
            <div>
              <h3 className="mb-1 text-[11px] font-semibold tracking-wide text-muted-foreground uppercase">
                {t(($) => $.node.input)}
              </h3>
              <p className="text-sm leading-relaxed">
                {runContext.decisionQuestion ?? t(($) => $.node.input_empty)}
              </p>
            </div>
            <div>
              <h3 className="mb-1 text-[11px] font-semibold tracking-wide text-muted-foreground uppercase">
                {t(($) => $.node.output)}
              </h3>
              <p className="text-sm leading-relaxed">
                {expectedResult ?? t(($) => $.node.output_empty)}
              </p>
            </div>
          </section>
        ) : null}

        {method || executor || runContext.task ? (
          <section className="rounded-lg border bg-muted/15 p-3">
            <div className="grid gap-3 sm:grid-cols-2">
              {method ? (
                <div className="sm:col-span-2">
                  <h3 className="mb-1 text-[11px] font-semibold tracking-wide text-muted-foreground uppercase">
                    {t(($) => $.node.method)}
                  </h3>
                  <p className="text-sm leading-relaxed">{method}</p>
                </div>
              ) : null}
              {executor ? (
                <div>
                  <h3 className="mb-1 text-[11px] font-semibold tracking-wide text-muted-foreground uppercase">
                    {t(($) => $.node.executor)}
                  </h3>
                  <p className="text-sm font-medium">{executor}</p>
                  {runContext.actor?.role ? (
                    <p className="text-xs text-muted-foreground">
                      {t(($) => $.node.executor_role)} {runContext.actor.role}
                    </p>
                  ) : null}
                </div>
              ) : null}
              {runContext.task ? (
                <div>
                  <h3 className="mb-1 text-[11px] font-semibold tracking-wide text-muted-foreground uppercase">
                    {t(($) => $.node.task_type)}
                  </h3>
                  <p className="text-sm font-medium">
                    {taskKindLabelFor(runContext.task.kind, t)}
                  </p>
                  <dl className="mt-1 space-y-0.5 text-xs text-muted-foreground">
                    <div className="flex gap-1">
                      <dt>{t(($) => $.node.required_role)}</dt>
                      <dd>{runContext.task.required_capability}</dd>
                    </div>
                    {expectedResult ? (
                      <div className="flex gap-1">
                        <dt>{t(($) => $.node.expected_result)}</dt>
                        <dd>{expectedResult}</dd>
                      </div>
                    ) : null}
                  </dl>
                </div>
              ) : null}
              {runContext.attempt ? (
                <div className="sm:col-span-2 flex flex-wrap gap-x-4 gap-y-1 border-t pt-2 text-xs text-muted-foreground">
                  <span>
                    {t(($) => $.node.attempt)} {runContext.attempt.attempt_number}
                  </span>
                  <span>{executionStatusLabelFor(runContext.attempt.status, t)}</span>
                  {runContext.task?.attempt_count && runContext.task.max_attempts ? (
                    <span>
                      {runContext.task.attempt_count}/{runContext.task.max_attempts}
                    </span>
                  ) : null}
                </div>
              ) : null}
            </div>
          </section>
        ) : null}

        {runContext.metrics.length > 0 || runContext.reportCreated ? (
          <section>
            <h3 className="mb-1.5 text-[11px] font-semibold tracking-wide text-muted-foreground uppercase">
              {t(($) => $.node.activity)}
            </h3>
            <div className="flex flex-wrap gap-1.5">
              {runContext.metrics.map((metric) => (
                <Badge key={metric.key} variant="secondary" className="text-[11px]">
                  {metric.label} {metric.value}
                </Badge>
              ))}
              {runContext.reportCreated ? (
                <Badge variant="secondary" className="text-[11px]">
                  {t(($) => $.node.report_created)}
                </Badge>
              ) : null}
            </div>
          </section>
        ) : null}

        {abandoned ? (
          <section
            className="rounded-md border border-dashed border-muted-foreground/40 bg-muted/40 px-3 py-2"
            data-testid="research-node-abandon-reason"
          >
            <h3 className="mb-1 text-[11px] font-semibold tracking-wide text-muted-foreground uppercase">
              {t(($) => $.node.abandon_reason)}
            </h3>
            <p className="whitespace-pre-wrap text-sm leading-relaxed text-foreground">
              {abandonReason ?? t(($) => $.node.abandon_reason_pending)}
            </p>
          </section>
        ) : null}

        {node.node_type === "dead_end" && deadEndReason ? (
          <section className="rounded-md border border-destructive/30 bg-destructive/5 px-3 py-2">
            <h3 className="mb-1 text-[11px] font-semibold tracking-wide text-destructive uppercase">
              {t(($) => $.node.dead_end_reason)}
            </h3>
            <p className="whitespace-pre-wrap text-sm leading-relaxed">{deadEndReason}</p>
          </section>
        ) : null}

        {runContext.attempt?.diagnostics && runContext.attempt.diagnostics !== deadEndReason ? (
          <section className="rounded-md border border-destructive/30 bg-destructive/5 px-3 py-2">
            <h3 className="mb-1 text-[11px] font-semibold tracking-wide text-destructive uppercase">
              {t(($) => $.node.diagnostics)}
            </h3>
            <p className="whitespace-pre-wrap text-sm leading-relaxed">
              {runContext.attempt.diagnostics}
            </p>
          </section>
        ) : null}

        {runContext.gateBlockers.length > 0 ? (
          <section className="rounded-md border border-destructive/30 bg-destructive/5 px-3 py-2">
            <h3 className="mb-1 text-[11px] font-semibold tracking-wide text-destructive uppercase">
              {t(($) => $.node.gate_blocker)}
            </h3>
            <ul className="space-y-1 text-sm leading-relaxed">
              {runContext.gateBlockers.map((blocker) => <li key={blocker}>{blocker}</li>)}
            </ul>
          </section>
        ) : null}

        {(() => {
          const step = runContext.nextStep;
          if (!step) return null;
          return (
            <section>
              <h3 className="mb-1 text-[11px] font-semibold tracking-wide text-muted-foreground uppercase">
                {t(($) => $.node.next_step)}
              </h3>
              <p className="text-sm leading-relaxed">{t(($) => $.node.next_steps[step])}</p>
            </section>
          );
        })()}

        {runContext.producedClaims.length > 0 ||
        runContext.createdQuestions.length > 0 ? (
          <section>
            <h3 className="mb-1.5 text-[11px] font-semibold tracking-wide text-muted-foreground uppercase">
              {t(($) => $.node.artifacts)}
            </h3>
            <ul className="space-y-2">
              {runContext.producedClaims.slice(0, 6).map((claim) => (
                <li key={claim.id} className="rounded-md border bg-muted/20 px-2.5 py-2">
                  <Badge variant="outline" className="mb-1 text-[10px]">
                    {t(($) => $.node.artifact_claim)}
                  </Badge>
                  <p className="text-xs font-medium">{claim.text}</p>
                  <p className="mt-1 text-[10px] text-muted-foreground">
                    {claim.status} · {(claim.confidence * 100).toFixed(0)}%
                  </p>
                </li>
              ))}

              {runContext.createdQuestions.slice(0, 6).map((question) => (
                <li key={question.id} className="rounded-md border bg-muted/20 px-2.5 py-2">
                  <Badge variant="outline" className="mb-1 text-[10px]">
                    {t(($) => $.node.artifact_question)}
                  </Badge>
                  <p className="text-xs font-medium">{question.question}</p>
                </li>
              ))}
            </ul>
          </section>
        ) : null}

        <section>
          <h3 className="mb-1.5 text-[11px] font-semibold tracking-wide text-muted-foreground uppercase">
            {t(($) => $.node.evidence)}
          </h3>
          {evidenceList.length === 0 ? (
            <p className="text-xs text-muted-foreground">{t(($) => $.node.evidence_empty)}</p>
          ) : (
            <ul className="space-y-2">
              {evidenceList.map((s) => (
                <li key={s.id} className="rounded-md border bg-muted/20 px-2.5 py-2">
                  <div className="flex items-start justify-between gap-2">
                    <a
                      href={s.url}
                      target="_blank"
                      rel="noreferrer"
                      className="min-w-0 truncate text-xs font-medium text-primary underline-offset-2 hover:underline"
                    >
                      {s.title || s.url}
                    </a>
                    <span className="shrink-0 font-mono text-[10px] text-muted-foreground">
                      {(s.credibility_weight ?? 0).toFixed(2)}
                    </span>
                  </div>
                  {s.excerpt ? (
                    <p className="mt-1 line-clamp-3 text-[11px] text-muted-foreground">
                      {s.excerpt}
                    </p>
                  ) : null}
                </li>
              ))}
            </ul>
          )}
        </section>

      </div>
    </>
  );
}

/**
 * LRM-797 / LRM-826:
 * - Desktop overlay-card: substantial detail card above Controls (not a tiny chip).
 * - Narrow: full-width bottom sheet.
 *
 * LRM-1290: overlay-card uses non-modal `<dialog open>` (not showModal), so it
 * lacks native Escape/focus — reuse `useOverlayPanelA11y` like desktop asides.
 * Narrow Sheet keeps Radix Esc/focus.
 */
export function ResearchNodeDetail({
  node,
  sources = EMPTY_SOURCES,
  run,
  members = EMPTY_MEMBERS,
  open = true,
  onClose,
  placement,
}: {
  node: ResearchGraphNode;
  sources?: ResearchSource[];
  run?: ResearchRunSnapshot;
  members?: ResearchFleetMember[];
  open?: boolean;
  onClose?: () => void;
  /** Force placement; default: overlay-card on desktop, sheet on narrow. */
  placement?: "overlay-card" | "sheet";
}) {
  const { t } = useT("research");
  const isMobile = useIsMobile();
  const mode = placement ?? (isMobile ? "sheet" : "overlay-card");
  const { bindPanel } = useOverlayPanelA11y({
    active: Boolean(open && mode === "overlay-card" && onClose),
    onClose: onClose ?? (() => {}),
  });

  if (!open) return null;

  if (mode === "overlay-card") {
    return (
      <dialog
        ref={bindPanel}
        open
        tabIndex={-1}
        data-testid="research-node-detail"
        data-placement="overlay-card"
        className="relative m-0 flex max-h-[min(68vh,640px)] w-[min(100%,420px)] translate-none flex-col overflow-hidden rounded-xl border bg-card/95 p-0 shadow-lg backdrop-blur-md outline-none open:flex"
        aria-label={t(($) => $.node.detail_hint)}
      >
        <div className="min-h-0 flex-1 overflow-y-auto">
          <DetailBody
            node={node}
            sources={sources}
            run={run}
            members={members}
            onClose={onClose}
            showClose
          />
        </div>
      </dialog>
    );
  }

  return (
    <Sheet
      open={open}
      onOpenChange={(next) => {
        if (!next) onClose?.();
      }}
    >
      <SheetContent
        side="bottom"
        className="max-h-[90vh] gap-0 overflow-y-auto p-0"
        data-testid="research-node-detail"
        data-placement="sheet"
      >
        <SheetHeader className="sr-only">
          <SheetTitle>{node.title}</SheetTitle>
          <SheetDescription>{t(($) => $.node.detail_hint)}</SheetDescription>
        </SheetHeader>
        <DetailBody node={node} sources={sources} run={run} members={members} onClose={onClose} />
      </SheetContent>
    </Sheet>
  );
}
