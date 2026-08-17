"use client";

import type {
  ResearchFleetMember,
  ResearchGraphEdge,
  ResearchGraphNode,
  ResearchNodeCommandAction,
  ResearchRunAttempt,
  ResearchRunSnapshot,
  ResearchRunTask,
  ResearchSource,
} from "@multica/core/types";
import { Badge } from "@multica/ui/components/ui/badge";
import { Button } from "@multica/ui/components/ui/button";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@multica/ui/components/ui/alert-dialog";
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
import { useMemo, useState, type ReactNode } from "react";
import { useT } from "../../i18n/use-t";
import { useOverlayPanelA11y } from "../hooks/use-overlay-panel-a11y";
import {
  buildDisputeModelForNode,
  DecisionDetailSection,
  DeliberationDetailSection,
  DisputeDetailSection,
  isDisputeDomainNodeType,
  PositionDetailSection,
  TurnDetailSection,
} from "../dispute";
import { isAbandonedStatus, readAbandonReason } from "../lib/abandon-reason";
import { safeSourceUrl } from "../report/safe-source-url";
import { normalizeNodeStatusKey, visualForNodeType } from "../lib/node-visuals";
import {
  ringActionsForNode,
  type NodeRingItem,
} from "../lib/node-action-ring";
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

function firstNumber(records: Record<string, unknown>[], key: string): number | null {
  for (const record of records) {
    const value = record[key];
    if (typeof value === "number" && Number.isFinite(value)) return value;
  }
  return null;
}

function parseISO(value: string | undefined | null): number | null {
  if (!value) return null;
  const ms = Date.parse(value);
  return Number.isNaN(ms) ? null : ms;
}

// Hoisted formatter (react-doctor/js-hoist-intl): build once, reuse across
// renders instead of rebuilding Intl.DateTimeFormat on every call.
const SHORT_DATETIME = new Intl.DateTimeFormat(undefined, {
  month: "short",
  day: "numeric",
  hour: "2-digit",
  minute: "2-digit",
});

/** Format an ISO timestamp as a short local date-time (guard against bad input). */
function formatTimestamp(value: string | undefined | null): string | null {
  const ms = parseISO(value);
  if (ms === null) return null;
  return SHORT_DATETIME.format(ms);
}

/** Human duration from milliseconds: "3m 12s" / "1h 4m" / "45s". */
function formatDuration(ms: number | null): string | null {
  if (ms === null || !Number.isFinite(ms) || ms < 0) return null;
  const totalSeconds = Math.floor(ms / 1000);
  if (totalSeconds < 1) return "<1s";
  const hours = Math.floor(totalSeconds / 3600);
  const minutes = Math.floor((totalSeconds % 3600) / 60);
  const seconds = totalSeconds % 60;
  if (hours > 0) return `${hours}h ${minutes}m`;
  if (minutes > 0) return `${minutes}m ${seconds}s`;
  return `${seconds}s`;
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
  attempts: ResearchRunAttempt[];
  actorID: string | null;
  actor: ResearchFleetMember | undefined;
  contributors: Array<{
    agentID: string;
    label: string;
    role: string | null;
  }>;
  objective: string | null;
  genericMethod: string | null;
  result: string | null;
  metrics: Array<{ key: string; label: string; value: number }>;
  reportCreated: boolean;
  producedSources: ResearchRunSnapshot["sources"];
  producedObservations: ResearchRunSnapshot["observations"];
  producedClaims: ResearchRunSnapshot["claims"];
  createdTasks: ResearchRunSnapshot["tasks"];
  createdQuestions: ResearchRunSnapshot["questions"];
  /** Session decision question (run.method.decision_question → contract.goal). */
  decisionQuestion: string | null;
  /** Projected node phase (payload.phase → node.phase → run.current_stage). */
  phase: string | null;
  /** Real run-engine start/update timestamps (never fabricated). */
  startedAt: string | null;
  updatedAt: string | null;
  /** Duration in ms between startedAt and end (completed or run last-progress). */
  durationMs: number | null;
  /** Explicit node input (task acceptance criteria / payload input). */
  input: string | null;
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
  const attemptID = firstString(records, ["attempt_id"]);
  const task = taskID ? run?.tasks.find((item) => item.id === taskID) : undefined;
  const attempts = taskID
    ? (run?.attempts ?? [])
        .filter((item) => item.task_id === taskID)
        .toSorted((a, b) => a.attempt_number - b.attempt_number)
    : [];
  const attempt =
    (attemptID ? attempts.find((item) => item.id === attemptID) : undefined) ??
    attempts.at(-1);
  const actorID =
    node.actor_agent_id ||
    firstString(records, ["agent_id", "actor_agent_id"]) ||
    task?.assigned_agent_id ||
    attempt?.assigned_agent_id ||
    null;
  const memberByAgentID = new Map(members.map((item) => [item.agent_id, item]));
  const actor = actorID ? memberByAgentID.get(actorID) : undefined;
  const contributorIDs = new Set<string>();
  if (actorID) contributorIDs.add(actorID);
  if (task?.assigned_agent_id) contributorIDs.add(task.assigned_agent_id);
  for (const item of attempts) {
    if (item.assigned_agent_id) contributorIDs.add(item.assigned_agent_id);
  }
  const contributors = [...contributorIDs].map((agentID) => {
    const member = memberByAgentID.get(agentID);
    return {
      agentID,
      label: actorLabel(member, agentID) ?? agentID,
      role: member?.role ?? null,
    };
  });
  const producedSources = taskID
    ? (run?.sources ?? []).filter((item) => item.produced_by_task_id === taskID)
    : [];
  const producedObservations = taskID
    ? (run?.observations ?? []).filter((item) => item.produced_by_task_id === taskID)
    : [];
  const producedClaims = taskID
    ? (run?.claims ?? []).filter((item) => item.produced_by_task_id === taskID)
    : [];
  const createdTasks = taskID
    ? (run?.tasks ?? []).filter((item) => item.parent_task_id === taskID)
    : [];
  const createdQuestions = taskID
    ? (run?.questions ?? []).filter((item) => item.created_by_task_id === taskID)
    : [];
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

  // Real run-engine timestamps only (LRM-1410 residual): never hardcode or
  // read local client clock into the header. Prefer task/attempt run fields,
  // then node created/updated, then run last-progress.
  const startedAt =
    task?.started_at ||
    attempt?.started_at ||
    firstString(records, ["started_at"]) ||
    node.created_at ||
    null;
  const updatedAt =
    task?.completed_at ||
    attempt?.completed_at ||
    firstString(records, ["completed_at", "updated_at"]) ||
    node.updated_at ||
    run?.run?.last_progress_at ||
    null;
  const startedMs = parseISO(startedAt);
  const endMs = parseISO(updatedAt) ?? startedMs;
  // Only surface a duration when there is a real positive delta — a node that
  // has not run (created == updated) must not show a fabricated "<1s".
  const durationMs =
    startedMs !== null && endMs !== null && endMs > startedMs
      ? endMs - startedMs
      : null;

  const decisionQuestion =
    run?.method?.decision_question || run?.contract?.goal || null;
  const phase =
    (typeof node.phase === "string" && node.phase.trim() ? node.phase : null) ||
    firstString(records, ["phase"]) ||
    (run?.run?.current_stage ? run.run.current_stage : null);

  // Explicit node input: task acceptance criteria (real BE contract), else
  // payload input text. Never invent a sentence when none exists.
  const explicitInput = firstString(records, ["input", "task_input", "inputs"]);
  const acceptanceCriteria = task?.acceptance_criteria;
  let input: string | null = explicitInput;
  if (!input && acceptanceCriteria && typeof acceptanceCriteria === "object") {
    try {
      const json = JSON.stringify(acceptanceCriteria);
      if (json && json !== "{}") input = json;
    } catch {
      input = null;
    }
  }
  if (!input && typeof acceptanceCriteria === "string" && acceptanceCriteria.trim()) {
    input = acceptanceCriteria.trim();
  }

  return {
    task,
    attempt,
    attempts,
    actorID,
    actor,
    contributors,
    objective:
      task?.objective || firstString(records, ["objective", "goal", "question", "small_goal"]),
    genericMethod: firstString(records, ["method", "approach", "strategy", "plan"]),
    result:
      firstString(records, ["result", "outcome", "conclusion"]) ||
      (node.summary?.trim() || null),
    metrics,
    reportCreated: Boolean(firstString(records, ["report_id"])),
    producedSources,
    producedObservations,
    producedClaims,
    createdTasks,
    createdQuestions,
    decisionQuestion,
    phase,
    startedAt,
    updatedAt,
    durationMs,
    input,
  };
}

function observationText(
  observation: ResearchRunSnapshot["observations"][number],
): string | null {
  if (observation.quote?.trim()) return observation.quote.trim();
  if (observation.interpretation?.trim()) return observation.interpretation.trim();
  if (typeof observation.datum === "string" && observation.datum.trim()) {
    return observation.datum.trim();
  }
  if (observation.datum && typeof observation.datum === "object") {
    try {
      return JSON.stringify(observation.datum);
    } catch {
      return null;
    }
  }
  return null;
}

type StructuredInputEntry = { key: string; value: string };

function parseStructuredInput(value: string): StructuredInputEntry[] | null {
  try {
    const parsed: unknown = JSON.parse(value);
    if (!parsed || typeof parsed !== "object" || Array.isArray(parsed)) return null;
    return Object.entries(parsed as Record<string, unknown>)
      .slice(0, 16)
      .map(([key, item]) => ({
        key,
        value:
          typeof item === "string"
            ? item
            : item === null || typeof item === "number" || typeof item === "boolean"
              ? String(item)
              : JSON.stringify(item),
      }));
  } catch {
    return null;
  }
}

function NodeInputDetail({ value }: { value: string }) {
  const { t } = useT("research");
  const entries = parseStructuredInput(value);
  return (
    <section data-testid="node-detail-input">
      <h3 className="mb-2 text-xs font-medium text-muted-foreground">
        {t(($) => $.node.input)}
      </h3>
      {entries ? (
        <dl className="grid grid-cols-[minmax(0,1fr)_auto] gap-x-3 gap-y-2 rounded-lg bg-muted/20 p-3 text-xs">
          {entries.map((entry) => (
            <div key={entry.key} className="contents">
              <dt className="min-w-0 break-words text-muted-foreground">
                {entry.key.replaceAll("_", " ")}
              </dt>
              <dd className="max-w-40 break-words text-right font-mono text-foreground">
                {entry.value}
              </dd>
            </div>
          ))}
        </dl>
      ) : (
        <details className="group rounded-lg bg-muted/20 p-3 text-xs">
          <summary className="cursor-pointer list-none text-foreground marker:hidden">
            <span className="line-clamp-4 whitespace-pre-wrap leading-relaxed group-open:hidden">
              {value}
            </span>
            <span className="mt-1 block text-muted-foreground group-open:hidden">
              {t(($) => $.node.raw_input_expand)}
            </span>
            <span className="hidden font-medium group-open:inline">
              {t(($) => $.node.raw_input_collapse)}
            </span>
          </summary>
          <pre className="mt-3 max-h-64 overflow-auto whitespace-pre-wrap break-words border-t pt-3 font-mono text-xs leading-relaxed text-muted-foreground">
            {value}
          </pre>
        </details>
      )}
    </section>
  );
}

function ExpandableObjective({ value }: { value: string }) {
  const { t } = useT("research");
  const isLong = value.length > 280 || value.split("\n").length > 6;
  if (!isLong) {
    return <p className="whitespace-pre-wrap text-sm leading-relaxed text-foreground">{value}</p>;
  }
  return (
    <details className="group">
      <summary className="cursor-pointer list-none marker:hidden">
        <span className="line-clamp-5 whitespace-pre-wrap text-sm leading-relaxed text-foreground group-open:hidden">
          {value}
        </span>
        <span className="mt-1 block text-xs text-muted-foreground group-open:hidden">
          {t(($) => $.node.objective_expand)}
        </span>
        <span className="hidden text-xs font-medium text-muted-foreground group-open:inline">
          {t(($) => $.node.objective_collapse)}
        </span>
      </summary>
      <p className="mt-2 whitespace-pre-wrap text-sm leading-relaxed text-foreground">{value}</p>
    </details>
  );
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

const GATE_NODE_TYPES = new Set(["stage_gate", "product_round_gate"]);

/**
 * LRM-1410 residual: next-step commands available for this node, derived from
 * the real status/type contract (mirrors card-menu action semantics). Read-only
 * display of the next-step slot — dispatch stays with the command console.
 */
type NextStepItem = { action: string; hint: string };

function nextStepsForNode(node: ResearchGraphNode): NextStepItem[] {
  const status = (node.status || "").toLowerCase();
  const running = status === "running" || status === "active" || status === "in_progress";
  const failed = status === "failed" || status === "error" || status === "terminal_failed";
  const retryable =
    failed ||
    status === "retryable_failed" ||
    status === "pending" ||
    status === "queued";
  const abandoned = status === "abandoned" || status === "cancelled";

  if (running) return [{ action: "continue", hint: "running" }];
  if (retryable) return [{ action: "retry", hint: "retry" }];
  if (abandoned) return [{ action: "reassign", hint: "reassign" }];
  if (GATE_NODE_TYPES.has(node.node_type)) {
    return [{ action: "continue", hint: "gate" }];
  }
  // Completed/done finding or subquestion → can be forked to explore deeper.
  if (status === "done" || status === "succeeded" || status === "completed" || status === "resolved") {
    return [
      { action: "continue", hint: "done" },
      { action: "fork", hint: "fork" },
    ];
  }
  return [];
}

const NEXT_STEP_ACTION_KEYS = {
  continue: "continue",
  fork: "fork",
  retry: "retry",
  reassign: "reassign",
} as const;

function nextStepActionLabel(
  action: string,
  t: ReturnType<typeof useT<"research">>["t"],
): string {
  const key = NEXT_STEP_ACTION_KEYS[action as keyof typeof NEXT_STEP_ACTION_KEYS];
  if (!key) return action;
  return t(($) => $.node.next_step_actions[key]);
}

export function ResearchNodeDetailBody({
  node,
  sources,
  run,
  members,
  graphNodes,
  graphEdges,
  onFocusNode,
  onClose,
  showClose,
  directorDetailSection,
}: {
  node: ResearchGraphNode;
  sources: ResearchSource[];
  run?: ResearchRunSnapshot;
  members: ResearchFleetMember[];
  graphNodes?: readonly ResearchGraphNode[];
  graphEdges?: readonly ResearchGraphEdge[];
  onFocusNode?: (nodeId: string) => void;
  onClose?: () => void;
  showClose?: boolean;
  directorDetailSection?: ReactNode;
}) {
  const { t } = useT("research");
  const visual = visualForNodeType(node.node_type);
  const typeLabel = typeLabelFor(node.node_type, t);
  const statusKey = normalizeNodeStatusKey(node.status);
  const statusLabel = t(($) => $.node.status[statusKey]);

  const sourceId = payloadString(node.payload, "source_id");
  const linked = sourceId ? sources.find((s) => s.id === sourceId) : undefined;
  const url = linked?.url || payloadString(node.payload, "url");
  const weight =
    linked?.credibility_weight ?? payloadNumber(node.payload, "credibility_weight");
  const sourceClass = linked?.source_class || payloadString(node.payload, "source_class");
  const confidence =
    payloadNumber(node.payload, "confidence") ??
    payloadNumber(node.payload, "confidence_score");
  const deadEndReason =
    payloadString(node.payload, "reason") ||
    payloadString(node.payload, "dead_end_reason") ||
    null;
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

  // Gate blocker: for stage_gate / product_round_gate nodes, surface the real
  // session run gate findings, plus any node-payload blocker text.
  const gateFindings: Array<{ code: string; severity: string; message: string }> =
    GATE_NODE_TYPES.has(node.node_type) && run?.gate && run.gate.findings.length > 0
      ? run.gate.findings.slice(0, 8)
      : [];
  if (GATE_NODE_TYPES.has(node.node_type) && gateFindings.length === 0) {
    const payloadBlocker = firstString([payloadRecord(node.payload)], ["blocker", "gate_blocked", "gate_blocker"]);
    if (payloadBlocker) {
      gateFindings.push({
        code: "payload",
        severity: "error",
        message: payloadBlocker,
      });
    }
  }
  const nextSteps = nextStepsForNode(node);
  const disputeModel = useMemo(
    () =>
      isDisputeDomainNodeType(node.node_type)
        ? buildDisputeModelForNode(graphNodes ?? [node], graphEdges ?? [], node.id)
        : null,
    [graphEdges, graphNodes, node],
  );

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
    <div
      data-testid="research-node-detail-body"
      className="min-w-0 [overflow-wrap:anywhere]"
    >
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
          {typeof confidence === "number" ? (
            <Badge variant="secondary" className="text-[10px]">
              {t(($) => $.node.confidence)}{" "}
              {(confidence <= 1 ? confidence * 100 : confidence).toFixed(0)}%
            </Badge>
          ) : null}
        </div>
        <h2 className="line-clamp-3 text-base font-medium leading-snug" title={node.title}>
          {node.title}
        </h2>
        <p className="sr-only">{t(($) => $.node.detail_hint)}</p>

        {/* LRM-1410 residual: real session-run header meta — phase, run
            timestamps and duration (never fabricated). flex-wrap keeps the
            row readable on both desktop and narrow sheet without overflow. */}
        {runContext.decisionQuestion ||
        runContext.phase ||
        runContext.startedAt ||
        runContext.durationMs !== null ? (
          <dl className="mt-2 flex flex-wrap items-center gap-x-3 gap-y-1 text-[11px] text-muted-foreground">
            {runContext.decisionQuestion ? (
              <div className="min-w-0" data-testid="node-detail-decision-question">
                <dt className="inline font-semibold">{t(($) => $.node.decision_question)} </dt>
                <dd className="inline min-w-0">{runContext.decisionQuestion}</dd>
              </div>
            ) : null}
            {runContext.phase ? (
              <div className="flex items-center gap-1">
                <dt className="font-semibold">{t(($) => $.node.phase_label)}</dt>
                <dd data-testid="node-detail-phase">{runContext.phase}</dd>
              </div>
            ) : null}
            {runContext.startedAt ? (
              <div className="flex items-center gap-1">
                <dt className="font-semibold">{t(($) => $.node.started_at)}</dt>
                <dd data-testid="node-detail-started">
                  {formatTimestamp(runContext.startedAt)}
                </dd>
              </div>
            ) : null}
            {runContext.updatedAt && runContext.updatedAt !== runContext.startedAt ? (
              <div className="flex items-center gap-1">
                <dt className="font-semibold">{t(($) => $.node.updated_at)}</dt>
                <dd data-testid="node-detail-updated">
                  {formatTimestamp(runContext.updatedAt)}
                </dd>
              </div>
            ) : null}
            {runContext.durationMs !== null ? (
              <div className="flex items-center gap-1">
                <dt className="font-semibold">{t(($) => $.node.duration)}</dt>
                <dd data-testid="node-detail-duration">
                  {formatDuration(runContext.durationMs)}
                </dd>
              </div>
            ) : null}
          </dl>
        ) : null}
      </header>

      <div className="space-y-4 p-4">
        {directorDetailSection}
        {disputeModel ? (
          <section data-testid="research-dispute-node-detail" className="rounded-lg border bg-muted/10 p-3">
            {node.node_type === "dispute" ? (
              <DisputeDetailSection model={disputeModel} onFocusNode={onFocusNode} />
            ) : node.node_type === "dispute_position" ? (
              <PositionDetailSection node={node} model={disputeModel} onFocusNode={onFocusNode} />
            ) : node.node_type === "deliberation" ? (
              <DeliberationDetailSection model={disputeModel} />
            ) : node.node_type === "deliberation_turn" ? (
              <TurnDetailSection node={node} />
            ) : node.node_type === "decision" ? (
              <DecisionDetailSection model={disputeModel} onFocusNode={onFocusNode} />
            ) : null}
          </section>
        ) : null}

        {/* LRM-1332: four content faces before run Objective/Method/Outcome. */}
        <ResearchNodeContentFaces node={node} density="detail" />

        {/* LRM-1410 residual: explicit Input block above Output/Result. */}
        {runContext.input ? (
          <NodeInputDetail value={runContext.input} />
        ) : null}

        {runContext.objective ? (
          <section>
            <h3 className="mb-1 text-[11px] font-semibold tracking-wide text-muted-foreground uppercase">
              {t(($) => $.node.objective)}
            </h3>
            <ExpandableObjective value={runContext.objective} />
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

        {runContext.contributors.length > 0 ? (
          <section data-testid="node-detail-contributors">
            <h3 className="mb-1.5 text-[11px] font-semibold tracking-wide text-muted-foreground uppercase">
              {t(($) => $.node.contributors)}
            </h3>
            <ul className="flex flex-wrap gap-1.5">
              {runContext.contributors.map((contributor) => (
                <li key={contributor.agentID}>
                  <Badge
                    variant="outline"
                    className="text-[11px]"
                    title={contributor.agentID}
                  >
                    {contributor.label}
                    {contributor.role ? ` · ${contributor.role}` : ""}
                  </Badge>
                </li>
              ))}
            </ul>
          </section>
        ) : null}

        {runContext.attempts.length > 1 ? (
          <section data-testid="node-detail-attempt-history">
            <h3 className="mb-1.5 text-[11px] font-semibold tracking-wide text-muted-foreground uppercase">
              {t(($) => $.node.attempt_history)}
            </h3>
            <ol className="space-y-1.5">
              {runContext.attempts.map((attempt) => {
                const contributor = runContext.contributors.find(
                  (item) => item.agentID === attempt.assigned_agent_id,
                );
                const timestampValue =
                  attempt.completed_at ??
                  attempt.result_submitted_at ??
                  attempt.started_at ??
                  attempt.dispatched_at ??
                  null;
                const timestamp = formatTimestamp(timestampValue);
                return (
                  <li
                    key={attempt.id}
                    className="flex flex-wrap items-center gap-x-2 gap-y-0.5 rounded-md border bg-muted/15 px-2.5 py-1.5 text-xs"
                  >
                    <span className="font-medium">
                      {t(($) => $.node.attempt)} {attempt.attempt_number}
                    </span>
                    <span>{executionStatusLabelFor(attempt.status, t)}</span>
                    <span className="text-muted-foreground">
                      {contributor?.label ?? actorLabel(undefined, attempt.assigned_agent_id)}
                    </span>
                    {timestamp ? (
                      <time
                        dateTime={timestampValue ?? undefined}
                        className="ml-auto text-[10px] text-muted-foreground"
                      >
                        {timestamp}
                      </time>
                    ) : null}
                  </li>
                );
              })}
            </ol>
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

        {/* LRM-1410 residual: gate blocker for stage_gate / product_round_gate
            nodes. Shows the real run gate findings (with severity) and a
            payload blocker; coexists with failure diagnostics below. */}
        {gateFindings.length > 0 ? (
          <section
            className="rounded-md border border-warning/40 bg-warning/5 px-3 py-2"
            data-testid="node-detail-gate-blocker"
          >
            <h3 className="mb-1 text-[11px] font-semibold tracking-wide text-warning uppercase">
              {t(($) => $.node.gate_blocker)}
            </h3>
            <ul className="space-y-1.5">
              {gateFindings.map((finding) => (
                <li key={finding.code} className="flex items-start gap-1.5 text-xs">
                  <span
                    className={`mt-px shrink-0 rounded px-1 font-mono text-[10px] uppercase ${
                      finding.severity === "error"
                        ? "bg-destructive/15 text-destructive"
                        : finding.severity === "warning"
                          ? "bg-warning/15 text-warning"
                          : "bg-muted text-muted-foreground"
                    }`}
                  >
                    {finding.severity}
                  </span>
                  <span className="min-w-0">{finding.message}</span>
                </li>
              ))}
            </ul>
          </section>
        ) : null}

        {/* LRM-1410 residual: read-only next-step commands for this node. */}
        {nextSteps.length > 0 ? (
          <section data-testid="node-detail-next-steps">
            <h3 className="mb-1.5 text-[11px] font-semibold tracking-wide text-muted-foreground uppercase">
              {t(($) => $.node.next_steps)}
            </h3>
            <div className="flex flex-wrap gap-1.5">
              {nextSteps.map((step) => (
                <Badge
                  key={step.action}
                  variant="outline"
                  className="text-[11px]"
                  data-testid={`node-detail-next-step-${step.action}`}
                >
                  {nextStepActionLabel(step.action, t)}
                </Badge>
              ))}
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

        {runContext.result ? (
          <section>
            <h3 className="mb-1 text-[11px] font-semibold tracking-wide text-muted-foreground uppercase">
              {node.status === "active" || node.status === "running"
                ? t(($) => $.node.doing)
                : t(($) => $.node.outcome)}
            </h3>
            <p className="whitespace-pre-wrap text-sm leading-relaxed text-foreground">
              {runContext.result}
            </p>
          </section>
        ) : (
          <p className="text-sm text-muted-foreground">{t(($) => $.node.summary_empty)}</p>
        )}

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

        {runContext.producedSources.length > 0 ||
        runContext.producedObservations.length > 0 ||
        runContext.producedClaims.length > 0 ||
        runContext.createdTasks.length > 0 ||
        runContext.createdQuestions.length > 0 ? (
          <section>
            <h3 className="mb-1.5 text-[11px] font-semibold tracking-wide text-muted-foreground uppercase">
              {t(($) => $.node.artifacts)}
            </h3>
            <ul className="space-y-2">
              {runContext.producedSources.slice(0, 6).map((source) => (
                <li key={source.id} className="rounded-md border bg-muted/20 px-2.5 py-2">
                  <Badge variant="outline" className="mb-1 text-[10px]">
                    {t(($) => $.node.artifact_source)}
                  </Badge>
                  <a
                    href={source.canonical_url}
                    target="_blank"
                    rel="noreferrer"
                    className="block truncate text-xs font-medium text-primary underline-offset-2 hover:underline"
                  >
                    {source.title || source.canonical_url}
                  </a>
                  {source.snapshot_excerpt ? (
                    <p className="mt-1 line-clamp-3 text-[11px] text-muted-foreground">
                      {source.snapshot_excerpt}
                    </p>
                  ) : null}
                </li>
              ))}
              {runContext.producedObservations.slice(0, 6).map((observation) => {
                const text = observationText(observation);
                return (
                  <li
                    key={observation.id}
                    className="rounded-md border bg-muted/20 px-2.5 py-2"
                  >
                    <Badge variant="outline" className="mb-1 text-[10px]">
                      {t(($) => $.node.artifact_observation)}
                    </Badge>
                    {text ? <p className="text-xs font-medium">{text}</p> : null}
                    {observation.locator ? (
                      <p className="mt-1 text-[10px] text-muted-foreground">
                        {observation.locator}
                      </p>
                    ) : null}
                  </li>
                );
              })}
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
              {runContext.createdTasks.slice(0, 6).map((task) => (
                <li key={task.id} className="rounded-md border bg-muted/20 px-2.5 py-2">
                  <Badge variant="outline" className="mb-1 text-[10px]">
                    {t(($) => $.node.artifact_task)}
                  </Badge>
                  <p className="text-xs font-medium">{task.objective}</p>
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
              {evidenceList.map((s) => {
                const href = safeSourceUrl(s.url);
                return (
                <li key={s.id} className="rounded-md border bg-muted/20 px-2.5 py-2">
                  <div className="flex items-start justify-between gap-2">
                    {href ? (
                      <a
                        href={href}
                        target="_blank"
                        rel="noreferrer noopener"
                        className="min-w-0 truncate text-xs font-medium text-primary underline-offset-2 hover:underline"
                      >
                        {s.title || s.url}
                      </a>
                    ) : (
                      <span className="min-w-0 truncate text-xs font-medium text-muted-foreground">
                        {s.title || s.url}
                      </span>
                    )}
                    {typeof s.credibility_weight === "number" ? (
                      <span className="shrink-0 font-mono text-[10px] text-muted-foreground">
                        {s.credibility_weight.toFixed(2)}
                      </span>
                    ) : null}
                  </div>
                  {s.excerpt ? (
                    <p className="mt-1 line-clamp-3 text-[11px] text-muted-foreground">
                      {s.excerpt}
                    </p>
                  ) : null}
                </li>
                );
              })}
            </ul>
          )}
        </section>

        {url && !evidenceList.some((s) => s.url === url) ? (
          <a
            href={url}
            target="_blank"
            rel="noreferrer"
            className="block truncate text-[11px] text-primary underline-offset-2 hover:underline"
          >
            {linked?.title || url}
          </a>
        ) : null}
      </div>
    </div>
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
  graphNodes,
  graphEdges,
  open = true,
  onClose,
  placement,
  onOpenReport,
  onNodeCommand,
  pendingNodeCommand = null,
  onFocusNode,
  directorDetailSection,
}: {
  node: ResearchGraphNode;
  sources?: ResearchSource[];
  run?: ResearchRunSnapshot;
  members?: ResearchFleetMember[];
  graphNodes?: readonly ResearchGraphNode[];
  graphEdges?: readonly ResearchGraphEdge[];
  open?: boolean;
  onClose?: () => void;
  /** Force placement; default: overlay-card on desktop, sheet on narrow. */
  placement?: "overlay-card" | "sheet" | "inline";
  onOpenReport?: () => void;
  onNodeCommand?: (action: ResearchNodeCommandAction) => void;
  pendingNodeCommand?: ResearchNodeCommandAction | null;
  onFocusNode?: (nodeId: string) => void;
  directorDetailSection?: ReactNode;
}) {
  const { t } = useT("research");
  const isMobile = useIsMobile();
  const [confirmReassign, setConfirmReassign] = useState(false);
  const mode = placement ?? (isMobile ? "sheet" : "overlay-card");
  const { bindPanel } = useOverlayPanelA11y({
    active: Boolean(open && mode === "overlay-card" && onClose),
    onClose: onClose ?? (() => {}),
  });

  if (!open) return null;

  if (mode === "inline") {
    const commandActions = onNodeCommand
      ? ringActionsForNode(node).filter(
          (action): action is NodeRingItem & { id: ResearchNodeCommandAction } =>
            ["continue", "fork", "retry", "reassign"].includes(action.id),
        )
      : [];
    const showActions = Boolean(onOpenReport || commandActions.length);
    const commandLabel = (action: ResearchNodeCommandAction) => {
      switch (action) {
        case "continue":
          return t(($) => $.ring.continue);
        case "fork":
          return t(($) => $.ring.fork);
        case "retry":
          return t(($) => $.ring.retry);
        case "reassign":
          return t(($) => $.ring.reassign);
      }
    };
    return (
      <>
        <div
          data-testid="research-node-detail"
          data-placement="inline"
          className="flex min-h-0 flex-1 flex-col overflow-hidden"
        >
          <ResearchNodeDetailBody
            node={node}
            sources={sources}
            run={run}
            members={members}
            graphNodes={graphNodes}
            graphEdges={graphEdges}
            onFocusNode={onFocusNode}
            onClose={onClose}
            showClose={Boolean(onClose)}
            directorDetailSection={directorDetailSection}
          />
        {showActions ? (
          <footer
            data-testid="research-node-detail-actions"
            className="flex shrink-0 flex-wrap gap-2 border-t border-border/70 bg-background/80 p-3"
          >
            {onOpenReport ? (
              <Button type="button" size="sm" onClick={onOpenReport}>
                {t(($) => $.d5.detail.open_report)}
              </Button>
            ) : null}
            {commandActions.map((action) => (
              <Button
                key={action.id}
                type="button"
                size="sm"
                variant={action.primary ? "default" : "outline"}
                aria-disabled={pendingNodeCommand !== null || undefined}
                className={
                  pendingNodeCommand !== null
                    ? "cursor-not-allowed opacity-50"
                    : undefined
                }
                onClick={() => {
                  if (pendingNodeCommand !== null) return;
                  if (action.id === "reassign") {
                    setConfirmReassign(true);
                    return;
                  }
                  onNodeCommand?.(action.id);
                }}
              >
                {pendingNodeCommand === action.id
                  ? t(($) => $.d5.detail.command_pending)
                  : commandLabel(action.id)}
              </Button>
            ))}
          </footer>
        ) : null}
        </div>
        <AlertDialog open={confirmReassign} onOpenChange={setConfirmReassign}>
          <AlertDialogContent>
            <AlertDialogHeader>
              <AlertDialogTitle>{t(($) => $.ring.reassign)}</AlertDialogTitle>
              <AlertDialogDescription>
                {t(($) => $.ring.reassign_confirm)}
              </AlertDialogDescription>
            </AlertDialogHeader>
            <AlertDialogFooter>
              <AlertDialogCancel>{t(($) => $.actions.cancel)}</AlertDialogCancel>
              <AlertDialogAction
                data-testid="research-node-reassign-confirm"
                onClick={() => {
                  setConfirmReassign(false);
                  onNodeCommand?.("reassign");
                }}
              >
                {t(($) => $.ring.reassign)}
              </AlertDialogAction>
            </AlertDialogFooter>
          </AlertDialogContent>
        </AlertDialog>
      </>
    );
  }

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
          <ResearchNodeDetailBody
            node={node}
            sources={sources}
            run={run}
            members={members}
            graphNodes={graphNodes}
            graphEdges={graphEdges}
            onFocusNode={onFocusNode}
            onClose={onClose}
            showClose
            directorDetailSection={directorDetailSection}
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
        <ResearchNodeDetailBody
          node={node}
          sources={sources}
          run={run}
          members={members}
          graphNodes={graphNodes}
          graphEdges={graphEdges}
          onFocusNode={onFocusNode}
          onClose={onClose}
          directorDetailSection={directorDetailSection}
        />
      </SheetContent>
    </Sheet>
  );
}
