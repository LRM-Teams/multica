"use client";

import { useMemo, useState, type ReactNode } from "react";
import {
  Activity,
  ArrowUpRight,
  Bot,
  BrainCircuit,
  CheckCircle2,
  CircleDollarSign,
  GitBranch,
  Lightbulb,
  LineChart,
  Play,
  Radio,
  RefreshCw,
  Save,
  ShieldCheck,
  Sparkles,
  TrendingUp,
  WandSparkles,
} from "lucide-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import { showErrorToast } from "@multica/ui/lib/error-toast";
import { api, ApiError } from "@multica/core/api";
import { useWorkspaceId } from "@multica/core/hooks";
import { useWorkspacePaths } from "@multica/core/paths";
import { agentListOptions, memberListOptions } from "@multica/core/workspace/queries";
import { deriveRuntimeHealth, runtimeListOptions } from "@multica/core/runtimes";
import { runtimeDisplayLabel } from "../../runtimes/components/runtime-machines";
import { useCurrentMember } from "@multica/core/permissions";
import {
  dashboardAgentRunTimeOptions,
  dashboardUsageByAgentOptions,
} from "@multica/core/dashboard";
import {
  evolutionKeys,
  evolutionMetricsOptions,
  evolutionReviewSubmissionListOptions,
  graphMemoryProfileOptions,
  memoryCurationRunOptions,
  memoryCuratorProfileOptions,
  workspaceMemoryCurationStatusOptions,
} from "@multica/core/evolution";
import type {
  Agent,
  AgentRuntime,
  DashboardAgentRunTime,
  DashboardUsageByAgent,
  EvolutionReviewSubmission,
  EvolutionReviewSubmissionStatus,
  EvolutionDailyMetric,
  EvolutionTaskEfficiency,
  EvolutionUnitMetric,
  GraphMemoryType,
  MemoryCurationRunDetail,
  MemoryCurationStageStatus,
  MemoryCuratorMode,
  MemoryCuratorProfile,
  WorkspaceMemoryCurationStatus,
} from "@multica/core/types";
import { Badge } from "@multica/ui/components/ui/badge";
import { Button, buttonVariants } from "@multica/ui/components/ui/button";
import { Tooltip, TooltipTrigger, TooltipContent } from "@multica/ui/components/ui/tooltip";
import { Checkbox } from "@multica/ui/components/ui/checkbox";
import {
  Card,
  CardContent,
  CardHeader,
  CardTitle,
} from "@multica/ui/components/ui/card";
import { Input } from "@multica/ui/components/ui/input";
import { Label } from "@multica/ui/components/ui/label";
import { Progress } from "@multica/ui/components/ui/progress";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@multica/ui/components/ui/select";
import { Switch } from "@multica/ui/components/ui/switch";
import { Skeleton } from "@multica/ui/components/ui/skeleton";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@multica/ui/components/ui/tabs";
import { cn } from "@multica/ui/lib/utils";
import { ActorAvatar } from "../../common/actor-avatar";
import { PageHeader } from "../../layout/page-header";
import { useT } from "../../i18n";
import { AppLink } from "../../navigation";
import {
  aggregateAgentTokens,
  formatDuration,
} from "../../dashboard/utils";
import { MemoryCurationDailyLedger } from "./memory-curation-daily-ledger";
import { AgentEvidencePanel } from "./agent-evidence-panel";
import {
  GraphMemoryAgentModeCard,
  GraphMemoryAuditCard,
  GraphMemoryConsolidationCard,
  GraphMemoryStatusCard,
  GraphMemoryTttCard,
  LegacyCurationNotApplicableCard,
  MemoryHealthCard,
  RetentionCard,
  TrainingGovernanceCard,
} from "./graph-memory-cards";

const COPY = {
  title: "Evolution Center",
  navLabel: "Self-evolution",
  eyebrow: "Autonomous workforce intelligence",
  heroTitle: "Watch the agent team learn, earn trust, and compound advantage.",
  heroBody:
    "A command room for agent performance, memory review, skill growth, cost efficiency, and curator health across the workspace.",
  runReview: "Open review queue",
  openAgents: "Open agents",
  liveSystem: "Live system",
  thirtyDays: "Last 30 days",
  metricRange: "Metrics range",
  agentTable: "Agents",
  agentColumn: "Agent",
  learningQueue: "Learning queue",
  learningQueueHint: "Review-first memory and skill candidates waiting for a human decision.",
  memoryOps: "Memory curation",
  memoryType: "Memory type",
  memoryTypeHint: "Pick the memory pipeline for this workspace. Graph memory builds a hierarchical memory graph and is experimental.",
  memoryTypeLegacy: "Legacy (MEMORY.md)",
  memoryTypeGraph: "Graph memory (experimental)",
  memoryTypeSaved: "Memory type updated",
  memoryTypeConflict: "Profile changed elsewhere, refreshed",
  graphStatus: "Graph memory status",
  graphStatusHint: "Per-graph versions, staging depth, and recall for this workspace.",
  graphEmptyStart: "Graph memory starts empty: no legacy project, channel, or daily memory was imported, and graph misses never fall back to it.",
  graphBackoff: "backoff",
  graphVersion: "version",
  graphStaging: "staging",
  graphRecall24h: "recalls (24h)",
  graphHitRate: "hit rate",
  graphAudit: "Graph memory audit",
  graphQueries24h: "queries (24h)",
  graphJudged24h: "judged (24h)",
  graphRegressions: "regressions",
  graphConsolidation: "Graph consolidation",
  graphConsolidationHint: "Manually consolidate staged graph memory. Retry by running again.",
  graphRunConsolidation: "Run consolidation",
  graphConsolidationQueued: "Graph consolidation queued",
  graphLastRun: "Last run",
  legacyCurationNotApplicable: "Legacy curation is not applicable",
  legacyCurationNotApplicableHint: "This workspace uses graph memory. Legacy L1-L4 curation, review candidates, and team-knowledge promotion do not run here; user and agent memory files are still maintained.",
  memoryTypeAdminOnly: "Only workspace owners and admins can change the memory type",
  memoryTypeGraphConfirmTitle: "Switch to graph memory (experimental)?",
  memoryTypeGraphConfirmBody: "Graph memory starts empty: existing project, channel, and daily memory is NOT imported, and graph misses never fall back to legacy project/channel/daily memory. User and agent memory files are unaffected.",
  memoryTypeGraphConfirmCheckbox: "I understand the empty start and no-fallback behavior",
  memoryTypeGraphConfirmApply: "Switch to graph",
  memoryTypeGraphConfirmCancel: "Cancel",
  graphAgentMode: "Graph memory delivery",
  graphAgentModeHint: "Choose a visible channel Memory Agent or synchronous injection. The modes never run together.",
  graphAgentModeAgent: "Memory Agent",
  graphAgentModeInject: "Inject before turns",
  graphAgentRuntime: "Memory Agent runtime",
  graphAgentRuntimePlaceholder: "Auto-select an eligible owner Pi runtime",
  graphAgentModel: "Memory Agent model",
  graphAgentTokensPerHour: "Workspace tokens per hour",
  graphAgentModeSave: "Save delivery settings",
  graphAgentModeSaved: "Graph memory delivery updated",
  graphAgentModeUnavailable: "Graph memory profile is unavailable",
  graphTtt: "Test-time training",
  graphRecallTtt: "Recall TTT",
  graphConsolidationTtt: "Consolidation TTT",
  graphRecallTttAgentDisabled: "Agent mode always uses one trajectory in this version",
  graphTttHint: "When on, each recall runs K independent Explore trajectories and adopts the fastest success, and graph consolidation generates multiple candidate versions in parallel and adopts the best. When off, both run exactly one trajectory.",
  graphTttConcurrency: "TTT concurrency",
  graphTttEffectiveK: "Effective K = 1 while TTT is off",
  graphTttSave: "Save TTT settings",
  graphTttSaved: "Graph TTT settings saved",
  graphTttConflict: "Profile changed elsewhere, refreshed",
  graphTttParseError: "Profile could not be parsed",
  graphTttRetry: "Retry",
  graphTttAdminOnly: "Only workspace owners and admins can change TTT settings",
  graphTrainingGovernance: "Training governance",
  graphTrainingGovernanceHint: "Tenant training only runs after the workspace owner acknowledges it; pooled training needs a separate explicit opt-in.",
  graphTrainingStatusPendingOwnerAck: "Awaiting owner acknowledgement",
  graphTrainingStatusActive: "Active",
  graphTrainingStatusRevoked: "Revoked",
  graphTrainingStatusDisabled: "Disabled",
  graphTrainingPolicyVersion: "v{version}",
  graphTrainingAck: "Acknowledge tenant training",
  graphTrainingAckSaved: "Tenant training acknowledged",
  graphTrainingRevoke: "Revoke training",
  graphTrainingRevoked: "Training grant revoked",
  graphTrainingSelection: "Training selection",
  graphTrainingExecution: "Training execution",
  graphTrainingPolicySaved: "Training policy updated",
  graphTrainingConflict: "Training governance changed elsewhere, refreshed",
  graphTrainingAdminOnly: "Only workspace owners and admins can manage training governance",
  graphRetention: "Memory retention",
  graphRetentionHint: "How long interaction trajectories, archives and traces stay hot, bounded by the platform caps.",
  graphRetentionVersion: "Policy version",
  graphRetentionCaps: "Platform caps",
  graphRetentionTrajectoryHot: "Trajectory hot days",
  graphRetentionArchive: "Archive days",
  graphRetentionTraceHot: "Trace hot days",
  graphRetentionSave: "Save retention policy",
  graphRetentionSaved: "Retention policy updated",
  graphRetentionConflict: "Retention policy changed elsewhere, refreshed",
  graphRetentionCapError: "Values exceed the platform caps",
  graphHealth: "Memory pipeline health",
  graphHealthHint: "Ledger-backed view of publish backlog, consolidation, migration and reward delivery.",
  graphHealthStaging: "staging backlog",
  graphHealthBackoff: "consolidation backoff",
  graphHealthRecallErrors: "recall errors",
  graphHealthOutboxFailed: "reward delivery failed",
  graphHealthDiveFailed: "dive jobs failed",
  graphHealthClean: "No ledger failures in the last 24 hours",
  memoryOpsHint: "Active agents self-review first; team curation then promotes clean shared knowledge. The top three stats are the workspace funnel: local proposals → DB pending → team knowledge.",
  funnelHint: "Funnel: left = latest self-review candidates; middle = DB pending candidates ({skills} skills); right = team_knowledge registry rows (not copied to every agent).",
  curatorOps: "Curator operations",
  curatorOpsHint: "Multi-agent curation, promotion, sharing, and safety checks.",
  starAgents: "Star agents",
  needsCoaching: "Needs coaching",
  memoryReview: "Memory review",
  skillDrafts: "Skill drafts",
  costEfficiency: "Cost efficiency",
  collaboration: "Collaboration",
  auditTrail: "Decision audit",
  successRate: "Success rate",
  tasks: "Tasks",
  issues: "Issues",
  cost: "Cost",
  costPerSuccess: "Cost / success",
  learned: "Learned",
  runtime: "Runtime",
  failures: "Failures",
  noAgents: "No agent activity yet.",
  noCandidates: "No evolution candidates in this filter.",
  promotion: "Promotion",
  evidence: "Evidence",
  source: "Source",
  confidence: "Confidence",
  sensitivity: "Sensitivity",
  pending: "Pending",
  promoted: "Promoted",
  rejected: "Rejected",
  candidates: "Candidates",
  memory: "Memory",
  skill: "Skill",
  workflow: "Workflow",
  preference: "Preference",
  all: "All",
  tabOverview: "Overview",
  tabAgents: "Agents",
  tabLearning: "Learning",
  tabMemory: "Memory",
  tabOps: "Ops",
  healthy: "Healthy",
  attention: "Attention",
  protected: "Review-first protected",
  autoDrafts: "Disabled drafts only",
  privateScope: "Agent-private by default",
  curated: "Curated",
  sharedMemory: "Promoted shared memory",
  sharedCandidates: "Awaiting shared review",
  localPromotion: "Agent self-review proposals",
  sharedPromotion: "Shared proposal",
  ingestion: "Ingestion",
  review: "Review",
  approve: "Approve",
  enable: "Enable",
  feedback: "Feedback",
  beijingTime: "Beijing time",
  dbEvidence: "DB evidence",
  semanticDedupe: "Local semantic dedupe",
  successSignals: "Success signals",
  used: "Used",
  insight1: "High-signal candidates become proposals, not silent memories.",
  insight2: "Skill drafts stay disabled until explicitly enabled for an agent.",
  insight3: "Shared knowledge is queued with provenance, scope, and safety metadata.",
  insight4: "Active agents write local daily/review/proposals; team curation promotes clean shared knowledge.",
  lastRun: "Latest curation run",
  projected: "Applied feedback",
  running: "Running",
  succeeded: "Succeeded",
  failed: "Failed",
  notRun: "Not run yet",
  unavailable: "Status unavailable",
  agentsProcessed: "agents scanned",
  agentsChanged: "changed",
  workspaceUnit: "Workspace unit",
  notBroadcast: "Shared units stay in the workspace registry and are not copied into every agent automatically.",
  evidenceCollected: "evidence",
  candidatesAdded: "candidates",
  archived: "archived",
  merged: "merged",
  curatorProfile: "Curator profile",
  curatorProfileHint: "Agent self-review is system scheduled for agents with prior-day material. Admins only choose the workspace curator runtime and agent for shared governance.",
  curatorAgent: "Curator agent",
  targetAgents: "Target agents",
  allMyAgents: "All my agents",
  selectedAgents: "Selected agents",
  mode: "Promotion mode",
  observeOnly: "Observe only",
  manualReview: "Manual review",
  autoSafe: "Auto-safe",
  fullAuto: "Full auto",
  schedule: "Daily start",
  timezone: "Timezone",
  automatic: "Nightly team curation",
  catchUp: "Catch up missed schedules",
  saveProfile: "Save profile",
  profileSaved: "Curator profile saved",
  configureProfile: "Select a runtime and curator agent first, then enable nightly team curation.",
  manualRun: "Manual run",
  manualRunHint: "Queue workspace team curation for active agents that already produced self-review material.",
  backfillRun: "Backfill missed days",
  backfillRunHint: "Queue missed team curation for active days in the last month. Idle days and agents with no reviewable material are skipped.",
  backfillSince: "From",
  backfillUntil: "To",
  queueBackfill: "Queue backfill",
  backfillQueued: "Backfill queued",
  allStages: "Self-review + team curation",
  automaticSelfReview: "Automatic self-review",
  automaticSelfReviewHint: "Every day the system sends self-review work only to agents with prior-day conversations, tasks, daily/memory writes, candidates, memory sync changes, or skill suggestions. Idle agents are skipped so the workspace does not spend tokens on empty reviews.",
  curatorConfigTitle: "Workspace curator",
  stage: "Stage",
  dryRun: "Dry run",
  queueRun: "Queue run",
  runQueued: "Curation run queued",
  saveProfileForStage: "Save the profile to enable this run stage.",
  selfReviewLabel: "Agent self-review",
  teamCurationLabel: "Team curation",
  selectRuntime: "Select runtime",
  selectCuratorAgent: "Select curator agent",
  modelOverride: "Model override",
  runtimeDefault: "Runtime default",
  confidenceThresholdLabel: "Confidence threshold",
  activeAgentSelfReview: "Active agent self-review",
  dailyReviewProposalFiles: "Daily, review, and proposal files",
  teamPromotion: "Team promotion",
  dedupedTeamKnowledge: "Deduped team knowledge and shared skills",
  stageStatus: "Status",
  curator: "Curator",
  claimAge: "Claim age",
  attempt: "Attempt",
  runtimeLastSeen: "Runtime last seen",
  threshold: "Threshold",
  promotedSharedUnits: "Promoted shared units",
  teamItems: "team items",
  conflicts: "conflicts",
  curationRunSelectHint: "Select a curation run to inspect runtime, timeline, per-agent results, and artifacts.",
  curationRunDetail: "Curation run detail",
  diagnosticAction: "Action",
  noneRecorded: "None recorded",
  timeline: "Timeline",
  timelineDone: "Done",
  timelineFailed: "Failed",
  timelinePending: "Pending",
  timelineSkipped: "Skipped",
  timelineStarted: "Started",
  timelineUnknown: "Processing",
  selfReviewChildRuns: "Self-review child runs",
  noSelfReviewChildRuns: "No self-review child runs were recorded for this parent run.",
  perAgentResults: "Per-agent results",
  noPerAgentDetails: "No per-agent details were reported by this daemon.",
  artifacts: "Artifacts",
  evolutionOutputTrend: "Evolution output trend",
  evolutionOutputTrendHint: "Daily self-review memory/skill candidates, shared evolution promotions, and team_knowledge items from curator runs.",
  teamKnowledgeShort: "Team knowledge",
  noTrendData: "No trend data yet.",
  taskEfficiency: "Task efficiency",
  taskEfficiencyHint: "Issue-level duration and token averages with evolved-memory usage attribution.",
  statusError: "error",
  statusChanged: "changed",
  statusUnchanged: "unchanged",
  avgDuration: "Avg duration",
  inputTokensShort: "Input tok",
  outputTokensShort: "Output tok",
  withLearnedUnits: "With learned units",
  avgUnitsUsed: "Avg units used",
  skills: "Skills",
  dailyLedger: "Daily memory ledger",
  dailyLedgerHint: "Self-review memories/skills produced each day, and team knowledge promoted by the curator. Click a day to inspect each item.",
  // LRM-986 / 983 frozen copy — individual evidence chain
  evidenceWorkspaceAgents: "Workspace agents",
  evidenceSortHint: "Sorted by recent evidence-chain activity",
  evidenceChainComplete: "Chain complete",
  evidenceIndividualBadge: "Individual evidence",
  evidenceIndividualHint: "Shows what was written → promotion result → whether it was used. Not a metric wall.",
  evidenceStatWrite: "Writes",
  evidenceStatWriteHint: "Memory/Skill proposals (14d)",
  evidenceStatPromoted: "Promoted",
  evidenceStatPromotedHint: "Curation approved / enabled",
  evidenceStatUsed: "Used",
  evidenceStatUsedHint: "Cited in later tasks",
  evidenceChainTitle: "Evidence chain",
  evidenceEmptyTitle: "No evidence chain yet",
  evidenceEmptyBody:
    "When an agent writes experience to Memory/Skill and curation promotes it, a Write → Promote → Use chain appears here. Come back after a valuable task.",
  evidenceEmptyCta: "View learning queue",
  evidenceNodeWrite: "Write",
  evidenceNodePromote: "Promote",
  evidenceNodeUsed: "Used",
  evidenceNodePendingUse: "Awaiting use",
  evidenceNodeDetail: "Node detail",
  evidenceSelectNode: "Select a chain node to inspect fields and back-links.",
  evidenceFieldEvent: "Event type",
  evidenceFieldTitle: "Experience title",
  evidenceFieldKind: "Experience kind",
  evidenceFieldSource: "Write source",
  evidenceFieldDecision: "Promotion result",
  evidenceFieldTask: "Usage task",
  evidenceFieldTime: "Time",
  evidenceOpenAgent: "Open agent profile",
  evidenceOpenAgentHint: "Agent detail / memory entry",
  evidenceSubmissionRef: "Submission",
  evidenceRecentUsed: "Recently used",
  evidencePendingPromote: "Pending promotion · {count} candidate(s)",
  evidenceNoUseYet: "No usage events yet",
  evidenceNoWrites: "No writes yet",
  evidenceJustNow: "just now",
  evidenceFilterEmpty: "No chains match this filter.",
};

type EvolutionCopy = (key: keyof typeof COPY) => string;

export function useEvolutionCopy(): EvolutionCopy {
  const { t } = useT("evolution");
  return (key) => t(($) => $[key], { defaultValue: COPY[key] });
}

const STATUSES = [
  "needs_review",
  "candidate",
  "promoted",
  "rejected",
] as const satisfies EvolutionReviewSubmissionStatus[];

const DAYS = 30;
const METRIC_DAY_OPTIONS = [7, 30, 90] as const;
const VIEW_TZ = Intl.DateTimeFormat().resolvedOptions().timeZone || "UTC";
const RUN_TIME_FORMATTER = new Intl.DateTimeFormat(undefined, {
  month: "short",
  day: "numeric",
  hour: "2-digit",
  minute: "2-digit",
});
const EMPTY_AGENTS: Agent[] = [];
const EMPTY_RUNTIME: DashboardAgentRunTime[] = [];
const EMPTY_USAGE_BY_AGENT: DashboardUsageByAgent[] = [];
const EMPTY_SUBMISSIONS: EvolutionReviewSubmission[] = [];
const EMPTY_UNIT_METRICS: EvolutionUnitMetric[] = [];
const MEMORY_CURATION_STAGE_LABELS = {
  selfReview: "selfReviewLabel",
  teamCuration: "teamCurationLabel",
} as const;
const MEMORY_CURATION_STAGES = [
  ["agent_self_review", MEMORY_CURATION_STAGE_LABELS.selfReview, "01:00", "activeAgentSelfReview", "dailyReviewProposalFiles"],
  ["team_curation", MEMORY_CURATION_STAGE_LABELS.teamCuration, "02:00", "teamPromotion", "dedupedTeamKnowledge"],
] as const;

type AgentEvolutionRow = {
  agent: Agent;
  taskCount: number;
  failedCount: number;
  successRate: number;
  cost: number;
  tokens: number;
  seconds: number;
  learnedCount: number;
  memoryCount: number;
  skillCount: number;
  score: number;
  costPerSuccess: number;
};

function money(value: number): string {
  if (!Number.isFinite(value) || value <= 0) return "$0";
  if (value >= 100) return `$${value.toFixed(0)}`;
  if (value >= 10) return `$${value.toFixed(1)}`;
  return `$${value.toFixed(2)}`;
}

function pct(value: number): string {
  if (!Number.isFinite(value)) return "0%";
  return `${Math.round(value * 100)}%`;
}

function compactNumber(value: number): string {
  if (!Number.isFinite(value) || value <= 0) return "0";
  if (value >= 1_000_000) return `${(value / 1_000_000).toFixed(1)}M`;
  if (value >= 1_000) return `${(value / 1_000).toFixed(1)}K`;
  return String(Math.round(value));
}

function shortId(value: string | null | undefined): string {
  if (!value) return "—";
  return value.length <= 8 ? value : value.slice(0, 8);
}

function normalizeUnitType(value: string | null | undefined): "memory" | "skill" | "workflow" | "preference" | "other" {
  const lower = (value ?? "").toLowerCase();
  if (lower.includes("skill")) return "skill";
  if (lower.includes("memory")) return "memory";
  if (lower.includes("preference")) return "preference";
  if (lower.includes("workflow") || lower.includes("tool")) return "workflow";
  return "other";
}

function isMemoryLikeUnitType(value: string | null | undefined): boolean {
  const normalized = normalizeUnitType(value);
  return normalized === "memory" || normalized === "preference" || normalized === "workflow";
}

function unitLabel(value: string | null | undefined, copy: EvolutionCopy): string {
  const normalized = normalizeUnitType(value);
  if (normalized === "memory") return copy("memory");
  if (normalized === "skill") return copy("skill");
  if (normalized === "workflow") return copy("workflow");
  if (normalized === "preference") return copy("preference");
  return value || copy("all");
}

function statusLabel(value: string, copy: EvolutionCopy): string {
  if (value === "promoted") return copy("promoted");
  if (value === "rejected") return copy("rejected");
  if (value === "candidate") return copy("candidates");
  return copy("pending");
}

function curationStatusLabel(value: string | undefined, copy: EvolutionCopy): string {
  if (value === "running" || value === "queued") return copy("running");
  if (value === "succeeded") return copy("succeeded");
  if (value === "failed") return copy("failed");
  return copy("notRun");
}

function curationTimelineStatusLabel(value: string, copy: EvolutionCopy): string {
  switch (value) {
    case "done": return copy("timelineDone");
    case "failed": return copy("timelineFailed");
    case "pending": return copy("timelinePending");
    case "running": return copy("running");
    case "skipped": return copy("timelineSkipped");
    case "started": return copy("timelineStarted");
    default: return copy("timelineUnknown"); // fail-closed, never raw
  }
}

function curationStageLabel(value: string, copy: EvolutionCopy): string {
  if (value === "all") return "ALL";
  const stageKey = MEMORY_CURATION_STAGES.find(([stage]) => stage === value)?.[1];
  return stageKey ? copy(stageKey) : value;
}

function formatRunTime(value: string | null | undefined, copy: EvolutionCopy): string {
  if (!value) return copy("notRun");
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return copy("notRun");
  return RUN_TIME_FORMATTER.format(date);
}

function formatRunDuration(run: MemoryCurationStageStatus | undefined): string | null {
  if (!run?.started_at || !run.finished_at) return null;
  const duration = new Date(run.finished_at).getTime() - new Date(run.started_at).getTime();
  if (!Number.isFinite(duration) || duration < 0) return null;
  if (duration < 1000) return "<1s";
  if (duration < 60_000) return `${Math.round(duration / 1000)}s`;
  return `${Math.round(duration / 60_000)}m`;
}

function scoreAgent(input: {
  successRate: number;
  taskCount: number;
  learnedCount: number;
  failedCount: number;
  cost: number;
}) {
  const throughput = Math.min(28, input.taskCount * 3.2);
  const quality = input.successRate * 42;
  const learning = Math.min(18, input.learnedCount * 4.5);
  const failurePenalty = Math.min(18, input.failedCount * 4.5);
  const costPenalty = Math.min(12, input.cost / 12);
  return Math.max(0, Math.round(quality + throughput + learning - failurePenalty - costPenalty));
}

function buildAgentRows(
  agents: Agent[],
  tokenRows: ReturnType<typeof aggregateAgentTokens>,
  runtimeRows: DashboardAgentRunTime[],
  submissions: EvolutionReviewSubmission[],
): AgentEvolutionRow[] {
  const tokenByAgent = new Map(tokenRows.map((row) => [row.agentId, row] as const));
  const runtimeByAgent = new Map(runtimeRows.map((row) => [row.agent_id, row] as const));
  const learningByAgent = new Map<string, { total: number; memory: number; skill: number }>();
  for (const submission of submissions) {
    const current = learningByAgent.get(submission.source_agent_id) ?? { total: 0, memory: 0, skill: 0 };
    current.total += 1;
    const unit = normalizeUnitType(submission.unit_type);
    if (unit === "skill") current.skill += 1;
    if (isMemoryLikeUnitType(submission.unit_type)) current.memory += 1;
    learningByAgent.set(submission.source_agent_id, current);
  }

  return agents.map((agent) => {
    const token = tokenByAgent.get(agent.id);
    const runtime = runtimeByAgent.get(agent.id);
    const learned = learningByAgent.get(agent.id) ?? { total: 0, memory: 0, skill: 0 };
    const taskCount = runtime?.task_count ?? token?.taskCount ?? 0;
    const failedCount = runtime?.failed_count ?? 0;
    const successCount = Math.max(0, taskCount - failedCount);
    const successRate = taskCount > 0 ? successCount / taskCount : 0;
    const cost = token?.cost ?? 0;
    return {
      agent,
      taskCount,
      failedCount,
      successRate,
      cost,
      tokens: token?.tokens ?? 0,
      seconds: runtime?.total_seconds ?? 0,
      learnedCount: learned.total,
      memoryCount: learned.memory,
      skillCount: learned.skill,
      costPerSuccess: successCount > 0 ? cost / successCount : cost,
      score: scoreAgent({ successRate, taskCount, learnedCount: learned.total, failedCount, cost }),
    };
  }).toSorted((a, b) => b.score - a.score || b.taskCount - a.taskCount || b.learnedCount - a.learnedCount);
}

export function EvolutionCenterPage() {
  const copy = useEvolutionCopy();
  const wsId = useWorkspaceId();
  const paths = useWorkspacePaths();
  const { userId, role } = useCurrentMember(wsId);
  const isWorkspaceAdmin = role === "owner" || role === "admin";
  const [learningFilter, setLearningFilter] = useState<"all" | "memory" | "skill">("all");
  const [selectedCurationRunId, setSelectedCurationRunId] = useState("");
  const [metricDays, setMetricDays] = useState<(typeof METRIC_DAY_OPTIONS)[number]>(30);
  const [activeTab, setActiveTab] = useState("overview");

  const { data: agentsData, isLoading: agentsLoading } = useQuery(agentListOptions(wsId));
  const { data: membersData } = useQuery(memberListOptions(wsId));
  const { data: runtimesData, isLoading: runtimesLoading } = useQuery(runtimeListOptions(wsId));
  const { data: profileData } = useQuery(memoryCuratorProfileOptions(wsId));
  const { data: gmProfile } = useQuery(graphMemoryProfileOptions(wsId));
  const isGraphMemory = gmProfile?.memory_type === "graph";
  const { data: usageData, isLoading: usageLoading } = useQuery(dashboardUsageByAgentOptions(wsId, DAYS, null, VIEW_TZ));
  const { data: runtimeData, isLoading: runtimeLoading } = useQuery(dashboardAgentRunTimeOptions(wsId, DAYS, null, VIEW_TZ));
  const { data: needsReviewData } = useQuery(evolutionReviewSubmissionListOptions(wsId, "needs_review"));
  const { data: candidateData } = useQuery(evolutionReviewSubmissionListOptions(wsId, "candidate"));
  const { data: promotedData } = useQuery(evolutionReviewSubmissionListOptions(wsId, "promoted"));
  const { data: rejectedData } = useQuery(evolutionReviewSubmissionListOptions(wsId, "rejected"));
  const { data: metricsData } = useQuery(evolutionMetricsOptions(wsId, metricDays));
  const {
    data: curationStatus,
    isLoading: curationStatusLoading,
    isError: curationStatusUnavailable,
  } = useQuery(workspaceMemoryCurationStatusOptions(wsId));
  const { data: selectedCurationRun } = useQuery(memoryCurationRunOptions(wsId, selectedCurationRunId));

  const agents = agentsData ?? EMPTY_AGENTS;
  const runtimes = runtimesData ?? [];
  const usageRows = usageData ?? EMPTY_USAGE_BY_AGENT;
  const runtimeRows = runtimeData ?? EMPTY_RUNTIME;
  const needsReviewSubmissions = needsReviewData ?? EMPTY_SUBMISSIONS;
  const candidateSubmissions = candidateData ?? EMPTY_SUBMISSIONS;
  const promotedSubmissions = promotedData ?? EMPTY_SUBMISSIONS;
  const rejectedSubmissions = rejectedData ?? EMPTY_SUBMISSIONS;
  const unitMetrics = metricsData?.unit_metrics ?? EMPTY_UNIT_METRICS;
  const dailyMetrics = metricsData?.daily_metrics ?? [];
  const taskEfficiency = metricsData?.task_efficiency;
  const collaborationMetrics = metricsData?.collaboration_evolution;
  const submissionsByStatus = useMemo(
    () => ({
      needs_review: needsReviewSubmissions,
      candidate: candidateSubmissions,
      promoted: promotedSubmissions,
      rejected: rejectedSubmissions,
    }),
    [candidateSubmissions, needsReviewSubmissions, promotedSubmissions, rejectedSubmissions],
  );
  const submissions = useMemo(
    () => STATUSES.flatMap((status) => submissionsByStatus[status] ?? EMPTY_SUBMISSIONS),
    [submissionsByStatus],
  );

  const rows = useMemo(
    () => buildAgentRows(agents, aggregateAgentTokens(usageRows), runtimeRows, submissions),
    [agents, runtimeRows, submissions, usageRows],
  );

  const totals = useMemo(() => {
    const taskCount = rows.reduce((sum, row) => sum + row.taskCount, 0);
    const failedCount = rows.reduce((sum, row) => sum + row.failedCount, 0);
    const cost = rows.reduce((sum, row) => sum + row.cost, 0);
    const learned = rows.reduce((sum, row) => sum + row.learnedCount, 0);
    const successRate = taskCount > 0 ? (taskCount - failedCount) / taskCount : 0;
    return {
      taskCount,
      failedCount,
      cost,
      learned,
      successRate,
      pending: submissionsByStatus.needs_review.length + submissionsByStatus.candidate.length,
      promoted: submissionsByStatus.promoted.length,
      skillDrafts: submissions.filter((item) => normalizeUnitType(item.unit_type) === "skill").length,
      memoryItems: submissions.filter((item) => isMemoryLikeUnitType(item.unit_type)).length,
      memoryUsed: unitMetrics.filter((item) => isMemoryLikeUnitType(item.unit_type)).reduce((sum, item) => sum + item.used_count, 0),
      skillUsed: unitMetrics.filter((item) => normalizeUnitType(item.unit_type) === "skill").reduce((sum, item) => sum + item.used_count, 0),
    };
  }, [rows, submissions, submissionsByStatus.candidate.length, submissionsByStatus.needs_review.length, submissionsByStatus.promoted.length, unitMetrics]);

  const filteredSubmissions = submissions.filter((submission) => {
    const unit = normalizeUnitType(submission.unit_type);
    if (learningFilter === "memory") return isMemoryLikeUnitType(submission.unit_type);
    if (learningFilter === "skill") return unit === "skill";
    return true;
  });

  const loading = agentsLoading || usageLoading || runtimeLoading;
  const topAgents = rows.slice(0, 5);
  const coachingRows = [...rows]
    .filter((row) => row.taskCount > 0 || row.failedCount > 0)
    .toSorted((a, b) => (b.failedCount - a.failedCount) || (a.successRate - b.successRate) || (b.cost - a.cost))
    .slice(0, 4);
  const stageByName = useMemo(
    () => new Map((curationStatus?.stages ?? []).map((stage) => [stage.stage, stage] as const)),
    [curationStatus?.stages],
  );
  const latestStage = (curationStatus?.stages ?? [])
    .toSorted((a, b) => Date.parse(b.created_at) - Date.parse(a.created_at))[0];
  const memorySubmissions = submissions.filter((item) => isMemoryLikeUnitType(item.unit_type));
  const sharedMemoryCandidates = curationStatus?.pending_candidates
    ?? memorySubmissions.filter((item) => item.status === "candidate" || item.status === "needs_review").length;
  const promotedSharedMemory = curationStatus?.team_knowledge_items
    ?? curationStatus?.promoted_candidates
    ?? memorySubmissions.filter((item) => item.status === "promoted").length;
  const curationHealth = curationStatusUnavailable
    ? copy("unavailable")
    : (curationStatus?.pending_runs ?? 0) > 0
      ? copy("running")
      : (curationStatus?.failed_runs_24h ?? 0) > 0
        ? copy("attention")
        : latestStage
          ? copy("healthy")
          : copy("notRun");

  return (
    <div className="flex min-h-0 flex-1 flex-col bg-[radial-gradient(circle_at_top_left,hsl(var(--brand)/0.18),transparent_28rem),linear-gradient(135deg,hsl(var(--background)),hsl(var(--muted)/0.35))]">
      <PageHeader className="justify-between border-border/60 bg-background/70 backdrop-blur-xl">
        <div className="flex items-center gap-2">
          <Sparkles className="h-4 w-4 text-brand" />
          <h1 className="text-sm font-medium">{copy("title")}</h1>
          <Badge variant="secondary" className="hidden md:inline-flex">{copy("liveSystem")}</Badge>
        </div>
        <div className="hidden items-center gap-2 sm:flex">
          <Select value={String(metricDays)} onValueChange={(value) => setMetricDays(Number(value) as (typeof METRIC_DAY_OPTIONS)[number])}>
            <SelectTrigger className="h-8 w-[8.5rem] bg-background/80 text-xs" aria-label={copy("metricRange")}>
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {METRIC_DAY_OPTIONS.map((days) => (
                <SelectItem key={days} value={String(days)}>{copy("thirtyDays").replace("30", String(days))}</SelectItem>
              ))}
            </SelectContent>
          </Select>
          <AppLink href={paths.agents()} className={buttonVariants({ variant: "outline", size: "sm" })}>
            {copy("openAgents")}
          </AppLink>
          <AppLink href={paths.skills()} className={buttonVariants({ size: "sm", className: "gap-1.5" })}>
            {copy("runReview")}<ArrowUpRight className="h-3.5 w-3.5" />
          </AppLink>
        </div>
      </PageHeader>

      <div className="min-h-0 flex-1 overflow-y-auto p-4 md:p-6">
        <div className="mx-auto flex max-w-7xl flex-col gap-5">
          <section className="relative overflow-hidden rounded-[2rem] border border-white/20 bg-foreground text-background shadow-2xl shadow-foreground/10">
            <div className="absolute inset-0 opacity-80 [background:radial-gradient(circle_at_15%_25%,rgba(255,255,255,.24),transparent_16rem),radial-gradient(circle_at_85%_15%,rgba(74,222,128,.26),transparent_18rem),linear-gradient(135deg,#0f172a,#111827_52%,#2f1600)]" />
            <div className="absolute -right-20 -top-20 h-72 w-72 rounded-full border border-white/20" />
            <div className="absolute bottom-0 left-1/2 h-40 w-[32rem] -translate-x-1/2 rounded-t-full bg-white/10 blur-3xl" />
            <div className="relative grid gap-8 p-6 md:grid-cols-[1.2fr_.8fr] md:p-8">
              <div className="space-y-5">
                <Badge className="bg-white/15 text-white ring-1 ring-white/20">{copy("eyebrow")}</Badge>
                <div className="max-w-3xl space-y-3">
                  <h2 className="text-3xl font-semibold tracking-tight text-white md:text-5xl">{copy("heroTitle")}</h2>
                  <p className="max-w-2xl text-sm leading-6 text-white/72 md:text-base">{copy("heroBody")}</p>
                </div>
                <div className="grid max-w-3xl gap-3 sm:grid-cols-3">
                  <SignalPill icon={ShieldCheck} label={copy("protected")} value={copy("review")} />
                  <SignalPill icon={WandSparkles} label={copy("autoDrafts")} value={copy("skillDrafts")} />
                  <SignalPill icon={GitBranch} label={copy("privateScope")} value={copy("curated")} />
                </div>
              </div>
              <div className="grid content-end gap-3">
                <HeroMetric label={copy("tasks")} value={String(totals.taskCount)} detail={copy("thirtyDays")} />
                <HeroMetric label={copy("successRate")} value={pct(totals.successRate)} detail={`${totals.failedCount} ${copy("failures").toLowerCase()}`} />
                <HeroMetric label={copy("cost")} value={money(totals.cost)} detail={`${totals.pending} ${copy("pending").toLowerCase()}`} />
              </div>
            </div>
          </section>

          <div className="grid gap-4 md:grid-cols-2 xl:grid-cols-4">
            <MetricCard icon={Bot} label={copy("starAgents")} value={String(topAgents.length)} detail={topAgents[0]?.agent.display_name ?? topAgents[0]?.agent.name ?? copy("noAgents")} tone="emerald" />
            <MetricCard icon={BrainCircuit} label={copy("memoryReview")} value={String(totals.memoryItems)} detail={`${totals.pending} ${copy("pending").toLowerCase()}`} tone="blue" />
            <MetricCard icon={Lightbulb} label={copy("skillDrafts")} value={String(totals.skillDrafts)} detail={copy("autoDrafts")} tone="amber" />
            <MetricCard icon={CircleDollarSign} label={copy("costEfficiency")} value={money(totals.cost / Math.max(1, totals.taskCount - totals.failedCount))} detail={copy("costPerSuccess")} tone="rose" />
            <MetricCard icon={GitBranch} label={copy("collaboration")} value={String(collaborationMetrics?.collaboration_sessions ?? 0)} detail={`${collaborationMetrics?.full_execution_wakes ?? 0} turn/response grants · ${metricDays}d`} tone="blue" />
            <MetricCard icon={Radio} label={copy("attention")} value={String(collaborationMetrics?.attention_rounds ?? 0)} detail={`${pct(collaborationMetrics?.attention_silent_rate ?? 0)} silent · ${pct(collaborationMetrics?.full_execution_reduction_rate ?? 0)} saved`} tone="emerald" />
            <MetricCard icon={ShieldCheck} label={copy("auditTrail")} value={String(collaborationMetrics?.immutable_decision_audit_events ?? 0)} detail={`${collaborationMetrics?.unauthorized_public_sends_blocked ?? 0} blocked · ${pct(collaborationMetrics?.turn_order_violation_rate ?? 0)} turn risk`} tone="amber" />
          </div>

          <Tabs value={activeTab} onValueChange={setActiveTab} className="gap-4">
            <TabsList className="w-full justify-start overflow-x-auto bg-background/70 p-1 shadow-sm backdrop-blur md:w-fit">
              <TabsTrigger value="overview" className="px-3">{copy("tabOverview")}</TabsTrigger>
              <TabsTrigger value="agents" className="px-3">{copy("tabAgents")}</TabsTrigger>
              <TabsTrigger value="learning" className="px-3">{copy("tabLearning")}</TabsTrigger>
              <TabsTrigger value="memory" className="px-3">{copy("tabMemory")}</TabsTrigger>
              <TabsTrigger value="ops" className="px-3">{copy("tabOps")}</TabsTrigger>
            </TabsList>

            <TabsContent value="overview" className="grid gap-4 md:grid-cols-2">
              <LearningPulseCard submissions={submissions} />
              <CoachingCard rows={coachingRows} />
            </TabsContent>

            <TabsContent value="agents" className="grid gap-4">
              <AgentEvidencePanel
                agents={agents}
                submissions={submissions}
                unitMetrics={unitMetrics}
                loading={loading}
                onOpenLearning={() => setActiveTab("learning")}
              />
            </TabsContent>

            <TabsContent value="learning" className="grid gap-4 xl:grid-cols-[.75fr_1.25fr]">
              <LearningSummaryCard totals={totals} />
              <LearningQueueCard
                submissions={filteredSubmissions}
                filter={learningFilter}
                onFilterChange={setLearningFilter}
              />
            </TabsContent>

            <TabsContent value="memory" className="grid gap-4 xl:grid-cols-[.9fr_1.1fr]">
              <div className="grid gap-4">
                <MemoryTypeCard wsId={wsId} isAdmin={isWorkspaceAdmin} />
                <GraphMemoryAgentModeCard
                  wsId={wsId}
                  isAdmin={isWorkspaceAdmin}
                  runtimes={runtimes}
                  runtimesLoading={runtimesLoading}
                  members={membersData ?? []}
                  currentUserId={userId}
                />
                <GraphMemoryTttCard wsId={wsId} isAdmin={isWorkspaceAdmin} />
                <TrainingGovernanceCard wsId={wsId} isAdmin={isWorkspaceAdmin} />
                <RetentionCard wsId={wsId} isAdmin={isWorkspaceAdmin} />
                {isGraphMemory ? (
                  <>
                    <GraphMemoryStatusCard wsId={wsId} />
                    <GraphMemoryConsolidationCard wsId={wsId} isAdmin={isWorkspaceAdmin} />
                    <LegacyCurationNotApplicableCard />
                  </>
                ) : (
                  <>
                    <CuratorProfileCard
                      key={`${profileData?.id ?? "new"}-${profileData?.config_version ?? 0}`}
                      wsId={wsId}
                      userId={userId}
                      profile={profileData}
                      agents={agents}
                      runtimes={runtimes}
                    />
                    <MemoryCurationCard
                      status={curationStatus}
                      loading={curationStatusLoading}
                      unavailable={curationStatusUnavailable}
                      onSelectRun={setSelectedCurationRunId}
                    />
                    <MemoryCurationDailyLedger wsId={wsId} />
                    <CurationRunDetailCard run={selectedCurationRun} selectedRunId={selectedCurationRunId} />
                  </>
                )}
              </div>
              <div className="grid gap-4">
                {isGraphMemory && <GraphMemoryAuditCard wsId={wsId} />}
                {isGraphMemory && <MemoryHealthCard wsId={wsId} />}
                <EvolutionTrendCard dailyMetrics={dailyMetrics} />
                <TaskEfficiencyCard efficiency={taskEfficiency} />
                <UnitMetricsCard metrics={unitMetrics} />
              </div>
            </TabsContent>

            <TabsContent value="ops" className="grid gap-4 lg:grid-cols-3">
              {isGraphMemory ? (
                <LegacyCurationNotApplicableCard />
              ) : (
                <>
              <OpsCard
                icon={RefreshCw}
                title={copy("lastRun")}
                value={latestStage ? `${curationStageLabel(latestStage.stage, copy)} · ${formatRunTime(latestStage.finished_at ?? latestStage.created_at, copy)}` : copy("notRun")}
                detail={latestStage ? `${latestStage.stats.agents_scanned} ${copy("agentsProcessed")} · ${latestStage.stats.agents_changed} ${copy("agentsChanged")}` : copy("memoryOpsHint")}
                status={curationHealth}
              />
              <OpsCard
                icon={ShieldCheck}
                title={copy("review")}
                value={copy("protected")}
                detail={`${sharedMemoryCandidates} ${copy("sharedCandidates").toLowerCase()} · ${curationStatus?.failed_runs_24h ?? 0} ${copy("failures").toLowerCase()}`}
                status={(curationStatus?.failed_runs_24h ?? 0) > 0 ? copy("attention") : copy("healthy")}
              />
              <OpsCard icon={LineChart} title={copy("projected")} value={`${totals.memoryUsed}/${totals.skillUsed}`} detail={copy("successSignals")} status={totals.memoryUsed + totals.skillUsed > 0 ? copy("healthy") : copy("attention")} />
              <ProcessCard
                stageByName={stageByName}
                sharedCandidates={sharedMemoryCandidates}
                promotedSharedMemory={promotedSharedMemory}
                feedbackCount={totals.memoryUsed + totals.skillUsed}
              />
                </>
              )}
            </TabsContent>
          </Tabs>
        </div>
      </div>
    </div>
  );
}

function SignalPill({ icon: Icon, label, value }: { icon: typeof ShieldCheck; label: string; value: string }) {
  return (
    <div className="rounded-2xl border border-white/15 bg-white/10 p-3 backdrop-blur">
      <Icon className="h-4 w-4 text-white/80" />
      <div className="mt-3 text-xs text-white/58">{label}</div>
      <div className="mt-1 text-sm font-medium text-white">{value}</div>
    </div>
  );
}

function HeroMetric({ label, value, detail }: { label: string; value: string; detail: string }) {
  return (
    <div className="rounded-3xl border border-white/15 bg-white/12 p-4 text-white backdrop-blur-xl">
      <div className="text-xs uppercase tracking-[0.2em] text-white/50">{label}</div>
      <div className="mt-2 text-3xl font-semibold tabular-nums">{value}</div>
      <div className="mt-1 text-xs text-white/60">{detail}</div>
    </div>
  );
}

function MetricCard({ icon: Icon, label, value, detail, tone }: { icon: typeof Bot; label: string; value: string; detail: string; tone: "emerald" | "blue" | "amber" | "rose" }) {
  const tones = {
    emerald: "from-emerald-500/20 to-teal-500/5 text-emerald-600",
    blue: "from-sky-500/20 to-blue-500/5 text-sky-600",
    amber: "from-amber-500/25 to-orange-500/5 text-amber-600",
    rose: "from-rose-500/20 to-pink-500/5 text-rose-600",
  };
  return (
    <Card className="border-white/40 bg-background/80 shadow-sm backdrop-blur">
      <CardContent className="flex items-start gap-3">
        <div className={cn("rounded-2xl bg-gradient-to-br p-3", tones[tone])}>
          <Icon className="h-5 w-5" />
        </div>
        <div className="min-w-0 flex-1">
          <div className="text-xs text-muted-foreground">{label}</div>
          <div className="mt-1 text-2xl font-semibold tabular-nums">{value}</div>
          <div className="mt-1 truncate text-xs text-muted-foreground">{detail}</div>
        </div>
      </CardContent>
    </Card>
  );
}

function MiniStat({ label, value }: { label: string; value: string }) {
  return (
    <div className="rounded-xl bg-muted/50 px-3 py-2">
      <div className="text-[11px] text-muted-foreground">{label}</div>
      <div className="text-sm font-semibold tabular-nums">{value}</div>
    </div>
  );
}

function LearningPulseCard({ submissions }: { submissions: EvolutionReviewSubmission[] }) {
  const copy = useEvolutionCopy();
  const memory = submissions.filter((s) => isMemoryLikeUnitType(s.unit_type)).length;
  const skill = submissions.filter((s) => normalizeUnitType(s.unit_type) === "skill").length;
  const promoted = submissions.filter((s) => s.status === "promoted").length;
  const total = Math.max(1, submissions.length);
  return (
    <Card className="bg-background/85 backdrop-blur">
      <CardHeader>
        <CardTitle className="flex items-center gap-2"><BrainCircuit className="h-4 w-4 text-brand" />{copy("learningQueue")}</CardTitle>
        <p className="text-sm text-muted-foreground">{copy("learningQueueHint")}</p>
      </CardHeader>
      <CardContent className="space-y-4">
        <div className="grid grid-cols-3 gap-2">
          <MiniStat label={copy("memory")} value={String(memory)} />
          <MiniStat label={copy("skill")} value={String(skill)} />
          <MiniStat label={copy("promoted")} value={String(promoted)} />
        </div>
        <div className="space-y-2">
          <Progress value={(memory / total) * 100} className="h-2" />
          <Progress value={(skill / total) * 100} className="h-2 opacity-70" />
          <Progress value={(promoted / total) * 100} className="h-2 opacity-40" />
        </div>
        <div className="space-y-2 text-sm text-muted-foreground">
          <InsightLine icon={CheckCircle2} text={copy("insight1")} />
          <InsightLine icon={ShieldCheck} text={copy("insight2")} />
        </div>
      </CardContent>
    </Card>
  );
}

function CoachingCard({ rows }: { rows: AgentEvolutionRow[] }) {
  const copy = useEvolutionCopy();
  return (
    <Card className="bg-background/85 backdrop-blur">
      <CardHeader>
        <CardTitle className="flex items-center gap-2"><Activity className="h-4 w-4 text-rose-500" />{copy("needsCoaching")}</CardTitle>
      </CardHeader>
      <CardContent className="space-y-3">
        {rows.length === 0 ? <EmptyState text={copy("noAgents")} /> : rows.map((row) => (
          <div key={row.agent.id} className="flex items-center gap-3 rounded-2xl border bg-muted/20 p-3">
            <ActorAvatar actorType="agent" actorId={row.agent.id} size={30} showStatusDot />
            <div className="min-w-0 flex-1">
              <div className="truncate text-sm font-medium">{row.agent.display_name || row.agent.name}</div>
              <div className="text-xs text-muted-foreground">{row.failedCount} {copy("failures").toLowerCase()} {"·"} {money(row.cost)} {copy("cost").toLowerCase()}</div>
            </div>
            <Badge variant={row.failedCount > 0 ? "destructive" : "outline"}>{pct(row.successRate)}</Badge>
          </div>
        ))}
      </CardContent>
    </Card>
  );
}

function LearningSummaryCard({ totals }: { totals: { pending: number; promoted: number; memoryItems: number; skillDrafts: number; learned: number } }) {
  const copy = useEvolutionCopy();
  return (
    <Card className="bg-background/85 backdrop-blur">
      <CardHeader>
        <CardTitle className="flex items-center gap-2"><Lightbulb className="h-4 w-4 text-amber-500" />{copy("learningQueue")}</CardTitle>
      </CardHeader>
      <CardContent className="space-y-4">
        <div className="grid grid-cols-2 gap-3">
          <MiniStat label={copy("pending")} value={String(totals.pending)} />
          <MiniStat label={copy("promoted")} value={String(totals.promoted)} />
          <MiniStat label={copy("memory")} value={String(totals.memoryItems)} />
          <MiniStat label={copy("skill")} value={String(totals.skillDrafts)} />
        </div>
        <div className="rounded-2xl border bg-muted/30 p-4 text-sm text-muted-foreground">
          {copy("insight1")}
        </div>
      </CardContent>
    </Card>
  );
}

function LearningQueueCard({ submissions, filter, onFilterChange }: { submissions: EvolutionReviewSubmission[]; filter: "all" | "memory" | "skill"; onFilterChange: (filter: "all" | "memory" | "skill") => void }) {
  const copy = useEvolutionCopy();
  return (
    <Card className="bg-background/85 backdrop-blur">
      <CardHeader>
        <div className="flex flex-col gap-3 sm:flex-row sm:items-start sm:justify-between">
          <div>
            <CardTitle className="flex items-center gap-2"><Sparkles className="h-4 w-4 text-brand" />{copy("learningQueue")}</CardTitle>
            <p className="mt-1 text-sm text-muted-foreground">{copy("learningQueueHint")}</p>
          </div>
          <div className="inline-flex rounded-lg bg-muted p-1">
            {(["all", "memory", "skill"] as const).map((value) => (
              <button key={value} type="button" onClick={() => onFilterChange(value)} className={cn("rounded-md px-3 py-1 text-xs font-medium", filter === value ? "bg-background shadow-sm" : "text-muted-foreground")}>{value === "all" ? copy("all") : value === "memory" ? copy("memory") : copy("skill")}</button>
            ))}
          </div>
        </div>
      </CardHeader>
      <CardContent className="space-y-3">
        {submissions.length === 0 ? <EmptyState text={copy("noCandidates")} /> : submissions.slice(0, 8).map((submission) => <SubmissionCard key={submission.id} submission={submission} />)}
      </CardContent>
    </Card>
  );
}

function SubmissionCard({ submission }: { submission: EvolutionReviewSubmission }) {
  const copy = useEvolutionCopy();
  return (
    <div className="rounded-2xl border bg-card/70 p-4 transition-colors hover:border-brand/30">
      <div className="flex flex-col gap-3 sm:flex-row sm:items-start sm:justify-between">
        <div className="min-w-0">
          <div className="flex flex-wrap items-center gap-2">
            <Badge variant="secondary">{unitLabel(submission.unit_type, copy)}</Badge>
            <Badge variant={submission.status === "rejected" ? "destructive" : "outline"}>{statusLabel(submission.status, copy)}</Badge>
            {submission.confidence && <Badge variant="outline">{submission.confidence}</Badge>}
          </div>
          <div className="mt-2 font-medium">{submission.title || submission.summary || shortId(submission.id)}</div>
          <p className="mt-1 line-clamp-2 text-sm text-muted-foreground">{submission.summary || submission.review_reason || submission.content}</p>
        </div>
        <div className="flex shrink-0 items-center gap-2 text-xs text-muted-foreground">
          <ActorAvatar actorType="agent" actorId={submission.source_agent_id} size={24} />
          <span>{shortId(submission.source_agent_id)}</span>
        </div>
      </div>
      <div className="mt-3 flex flex-wrap gap-2 text-xs text-muted-foreground">
        <span>{copy("sensitivity")}: {submission.sensitivity || "—"}</span>
        <span>{"·"}</span>
        <span>{copy("source")}: {submission.bundle_ref || shortId(submission.local_unit_id)}</span>
      </div>
    </div>
  );
}

type CuratorProfileDraft = {
  teamCurationEnabled: boolean;
  mode: MemoryCuratorMode;
  runtimeId: string;
  curatorAgentId: string;
  timezone: string;
  scheduleHour: number;
  modelOverride: string;
  catchUpEnabled: boolean;
  confidenceThreshold: number;
};

function draftFromProfile(profile: MemoryCuratorProfile | undefined): CuratorProfileDraft {
  return {
    teamCurationEnabled: profile?.team_curation_enabled === true,
    mode: profile?.mode ?? "review",
    runtimeId: profile?.runtime_id ?? "",
    curatorAgentId: profile?.curator_agent_id ?? "",
    timezone: profile?.timezone || "Asia/Shanghai",
    scheduleHour: profile?.schedule_hour ?? 2,
    modelOverride: profile?.model_override ?? "",
    catchUpEnabled: profile?.catch_up_enabled ?? true,
    confidenceThreshold: profile?.confidence_threshold ?? 0.8,
  };
}

export function MemoryTypeCard({ wsId, isAdmin }: { wsId: string; isAdmin: boolean }) {
  const copy = useEvolutionCopy();
  const queryClient = useQueryClient();
  const { data: profile } = useQuery(graphMemoryProfileOptions(wsId));
  const memoryType: GraphMemoryType = profile?.memory_type ?? "legacy";
  const [pendingGraphConfirm, setPendingGraphConfirm] = useState(false);
  const [confirmed, setConfirmed] = useState(false);

  // The PUT endpoint guards concurrent writes with a config_version
  // compare-and-set (spec §16): every update to an existing row must carry
  // the current version or the server rejects it with 409. The remaining
  // tunables are preserved server-side when left out.
  // Switching TO graph requires the explicit empty-start confirmation.
  const update = useMutation({
    mutationFn: (next: GraphMemoryType) => api.updateGraphMemoryProfile(wsId, {
      memory_type: next,
      explore_agents: profile?.explore_agents ?? 4,
      explore_max_rounds: profile?.explore_max_rounds ?? 6,
      config_version: profile?.config_version ?? 0,
      ...(next === "graph" ? { confirm_empty_start: true } : {}),
    }),
    onSuccess: async () => {
      toast.success(copy("memoryTypeSaved"));
      setPendingGraphConfirm(false);
      setConfirmed(false);
      await queryClient.invalidateQueries({ queryKey: evolutionKeys.graphMemoryProfile(wsId) });
    },
    onError: (error) => {
      if (error instanceof ApiError && error.status === 409) {
        void queryClient.invalidateQueries({ queryKey: evolutionKeys.graphMemoryProfile(wsId) });
        showErrorToast(copy("memoryTypeConflict"));
        return;
      }
      showErrorToast(error instanceof Error ? error.message : copy("memoryType"));
    },
  });

  return (
    <Card className="bg-background/90 backdrop-blur">
      <CardHeader>
        <CardTitle className="flex items-center gap-2"><GitBranch className="h-4 w-4 text-brand" />{copy("memoryType")}</CardTitle>
        <p className="text-sm text-muted-foreground">{copy("memoryTypeHint")}</p>
      </CardHeader>
      <CardContent className="space-y-3">
        <Select
          value={memoryType}
          onValueChange={(value) => {
            if (!value || value === memoryType) return;
            if (value === "graph") setPendingGraphConfirm(true);
            else update.mutate("legacy");
          }}
          disabled={update.isPending || !isAdmin}
        >
          <SelectTrigger
            className="w-full"
            aria-label={copy("memoryType")}
            title={isAdmin ? undefined : copy("memoryTypeAdminOnly")}
          >
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="legacy">{copy("memoryTypeLegacy")}</SelectItem>
            <SelectItem value="graph">{copy("memoryTypeGraph")}</SelectItem>
          </SelectContent>
        </Select>
        {pendingGraphConfirm && (
          <div className="space-y-3 rounded-2xl border border-destructive/30 bg-destructive/5 p-3">
            <p className="text-sm">{copy("memoryTypeGraphConfirmTitle")}</p>
            <p className="text-xs text-muted-foreground">{copy("memoryTypeGraphConfirmBody")}</p>
            <label className="flex items-center gap-2 text-xs">
              <Checkbox checked={confirmed} onCheckedChange={(v) => setConfirmed(v === true)} />
              {copy("memoryTypeGraphConfirmCheckbox")}
            </label>
            <div className="flex gap-2">
              <Button
                size="sm"
                disabled={!confirmed || !isAdmin || update.isPending}
                onClick={() => update.mutate("graph")}
              >
                {copy("memoryTypeGraphConfirmApply")}
              </Button>
              <Button size="sm" variant="ghost" onClick={() => { setPendingGraphConfirm(false); setConfirmed(false); }}>
                {copy("memoryTypeGraphConfirmCancel")}
              </Button>
            </div>
          </div>
        )}
      </CardContent>
    </Card>
  );
}

function CuratorProfileCard({
  wsId,
  userId,
  profile,
  agents,
  runtimes,
}: {
  wsId: string;
  userId: string | null;
  profile: MemoryCuratorProfile | undefined;
  agents: Agent[];
  runtimes: AgentRuntime[];
}) {
  const copy = useEvolutionCopy();
  const queryClient = useQueryClient();
  const [draft, setDraft] = useState<CuratorProfileDraft>(() => draftFromProfile(profile));
  const [dryRun, setDryRun] = useState(false);
  const [backfillRange, setBackfillRange] = useState(() => ({
    since: defaultBackfillSince(),
    until: defaultBackfillUntil(),
  }));

  const availableRuntimes = runtimes;
  // Computed once per render, outside JSX, so react:doctor's
  // hydration-mismatch rule doesn't flag a fresh Date.now() per row.
  const now = Date.now();
  const ownedAgents = agents.filter((agent) => agent.owner_id === userId);
  const curatorAgents = ownedAgents.filter((agent) => agent.runtime_id === draft.runtimeId);
  const configured = !!profile?.id && !!profile.runtime_id && !!profile.curator_agent_id;
  const teamCurationRunnable = configured && profile?.team_curation_enabled === true;

  const save = useMutation({
    mutationFn: () => api.updateMemoryCuratorProfile(wsId, {
      enabled: draft.teamCurationEnabled,
      self_review_enabled: false,
      team_curation_enabled: draft.teamCurationEnabled,
      mode: draft.mode,
      runtime_id: draft.runtimeId,
      curator_agent_id: draft.curatorAgentId,
      target_scope: "owned_all",
      model_override: draft.modelOverride,
      target_agent_ids: [],
      timezone: draft.timezone,
      schedule_hour: draft.scheduleHour,
      catch_up_enabled: draft.catchUpEnabled,
      confidence_threshold: draft.confidenceThreshold,
    }),
    onSuccess: async () => {
      toast.success(copy("profileSaved"));
      await Promise.all([
        queryClient.invalidateQueries({ queryKey: evolutionKeys.memoryCuratorProfile(wsId) }),
        queryClient.invalidateQueries({ queryKey: evolutionKeys.memoryCurationStatus(wsId) }),
      ]);
    },
    onError: (error) => showErrorToast(error instanceof Error ? error.message : copy("configureProfile")),
  });
  const run = useMutation({
    mutationFn: () => api.startMemoryCurationRun(wsId, {
      all_agents: true,
      stage: "team_curation",
      dry_run: dryRun,
    }),
    onSuccess: async () => {
      toast.success(copy("runQueued"));
      await Promise.all([
        queryClient.invalidateQueries({ queryKey: evolutionKeys.memoryCuratorProfile(wsId) }),
        queryClient.invalidateQueries({ queryKey: evolutionKeys.memoryCurationStatus(wsId) }),
      ]);
    },
    onError: (error) => showErrorToast(error instanceof Error ? error.message : copy("configureProfile")),
  });
  const backfill = useMutation({
    mutationFn: () => api.startMemoryCurationBackfill(wsId, {
      since: backfillRange.since,
      until: backfillRange.until,
      dry_run: dryRun,
    }),
    onSuccess: async (result) => {
      toast.success(`${copy("backfillQueued")}: ${result.queued_days}`);
      await Promise.all([
        queryClient.invalidateQueries({ queryKey: evolutionKeys.memoryCuratorProfile(wsId) }),
        queryClient.invalidateQueries({ queryKey: evolutionKeys.memoryCurationStatus(wsId) }),
      ]);
    },
    onError: (error) => showErrorToast(error instanceof Error ? error.message : copy("configureProfile")),
  });

  return (
    <Card className="overflow-hidden border-brand/20 bg-background/90 backdrop-blur">
      <div className="h-1 bg-gradient-to-r from-emerald-500 via-cyan-500 to-amber-400" />
      <CardHeader>
        <div className="flex items-start justify-between gap-3">
          <div>
            <CardTitle className="flex items-center gap-2"><BrainCircuit className="h-4 w-4 text-brand" />{copy("curatorProfile")}</CardTitle>
            <p className="mt-1 text-sm text-muted-foreground">{copy("curatorProfileHint")}</p>
          </div>
          <div className="flex items-center gap-2">
            <Label htmlFor="curator-enabled" className="text-xs">{copy("automatic")}</Label>
            <Switch id="curator-enabled" checked={draft.teamCurationEnabled} onCheckedChange={(checked) => setDraft((current) => ({ ...current, teamCurationEnabled: checked }))} />
          </div>
        </div>
      </CardHeader>
      <CardContent className="space-y-5">
        <div className="rounded-lg border bg-muted/20 p-4">
          <div className="flex items-start gap-3">
            <CheckCircle2 className="mt-0.5 h-4 w-4 shrink-0 text-emerald-500" />
            <div>
              <div className="text-sm font-medium">{copy("automaticSelfReview")}</div>
              <p className="mt-1 text-xs leading-5 text-muted-foreground">{copy("automaticSelfReviewHint")}</p>
            </div>
          </div>
        </div>
        <div className="grid gap-3 sm:grid-cols-2">
          <div className="sm:col-span-2 text-sm font-medium">{copy("curatorConfigTitle")}</div>
          <Field label={copy("runtime")}>
            <Select value={draft.runtimeId} onValueChange={(value) => setDraft((current) => ({ ...current, runtimeId: value ?? "", curatorAgentId: "" }))}>
              <SelectTrigger className="w-full"><SelectValue placeholder={copy("selectRuntime")} /></SelectTrigger>
              <SelectContent>{availableRuntimes.map((runtime) => <SelectItem key={runtime.id} value={runtime.id}>{runtimeDisplayLabel(runtime)} · {deriveRuntimeHealth(runtime, now)}</SelectItem>)}</SelectContent>
            </Select>
          </Field>
          <Field label={copy("curatorAgent")}>
            <Select value={draft.curatorAgentId} onValueChange={(value) => setDraft((current) => ({ ...current, curatorAgentId: value ?? "" }))}>
              <SelectTrigger className="w-full"><SelectValue placeholder={copy("selectCuratorAgent")} /></SelectTrigger>
              <SelectContent>{curatorAgents.map((agent) => <SelectItem key={agent.id} value={agent.id}>{agent.display_name || agent.name}</SelectItem>)}</SelectContent>
            </Select>
          </Field>
          <Field label={copy("mode")}>
            <Select value={draft.mode} onValueChange={(value) => value && setDraft((current) => ({ ...current, mode: value as MemoryCuratorMode }))}>
              <SelectTrigger className="w-full"><SelectValue /></SelectTrigger>
              <SelectContent>
                <SelectItem value="observe">{copy("observeOnly")}</SelectItem><SelectItem value="review">{copy("manualReview")}</SelectItem><SelectItem value="auto_safe">{copy("autoSafe")}</SelectItem><SelectItem value="auto">{copy("fullAuto")}</SelectItem>
              </SelectContent>
            </Select>
          </Field>
          <Field label={copy("timezone")}><Input value={draft.timezone} onChange={(event) => setDraft((current) => ({ ...current, timezone: event.target.value }))} /></Field>
          <Field label={copy("schedule")}><Input type="number" min={0} max={23} value={draft.scheduleHour} onChange={(event) => setDraft((current) => ({ ...current, scheduleHour: Number(event.target.value) }))} /></Field>
          <Field label={copy("modelOverride")}><Input value={draft.modelOverride} placeholder={copy("runtimeDefault")} onChange={(event) => setDraft((current) => ({ ...current, modelOverride: event.target.value }))} /></Field>
          <Field label={copy("confidenceThresholdLabel")}><Input type="number" min={0} max={1} step={0.05} value={draft.confidenceThreshold} onChange={(event) => setDraft((current) => ({ ...current, confidenceThreshold: Number(event.target.value) }))} /></Field>
        </div>
        <div className="flex flex-wrap items-center gap-4">
          <Label htmlFor="curator-catch-up" className="flex items-center gap-2 text-xs"><Checkbox id="curator-catch-up" checked={draft.catchUpEnabled} onCheckedChange={(checked) => setDraft((current) => ({ ...current, catchUpEnabled: checked === true }))} />{copy("catchUp")}</Label>
          <Button onClick={() => save.mutate()} disabled={save.isPending || !draft.runtimeId || !draft.curatorAgentId} className="gap-2"><Save className="h-4 w-4" />{copy("saveProfile")}</Button>
        </div>
        <div className="rounded-2xl border bg-muted/20 p-4">
          <div className="flex flex-col gap-3 sm:flex-row sm:items-end sm:justify-between">
            <div><div className="text-sm font-medium">{copy("manualRun")}</div><p className="mt-1 text-xs text-muted-foreground">{copy("manualRunHint")}</p></div>
            <div className="flex flex-wrap items-center gap-2">
              <Badge variant="secondary">{copy(MEMORY_CURATION_STAGE_LABELS.teamCuration)}</Badge>
              <label className="flex items-center gap-2 text-xs"><Checkbox checked={dryRun} onCheckedChange={(checked) => setDryRun(checked === true)} />{copy("dryRun")}</label>
              <Button variant="outline" onClick={() => run.mutate()} disabled={!teamCurationRunnable || run.isPending || save.isPending || backfill.isPending} title={teamCurationRunnable ? undefined : copy("saveProfileForStage")} className="gap-2"><Play className="h-4 w-4" />{copy("queueRun")}</Button>
            </div>
          </div>
        </div>
        <div className="rounded-2xl border bg-muted/20 p-4">
          <div className="flex flex-col gap-3">
            <div><div className="text-sm font-medium">{copy("backfillRun")}</div><p className="mt-1 text-xs text-muted-foreground">{copy("backfillRunHint")}</p></div>
            <div className="flex flex-wrap items-end gap-2">
              <Field label={copy("backfillSince")}><Input type="date" value={backfillRange.since} onChange={(event) => setBackfillRange((current) => ({ ...current, since: event.target.value }))} /></Field>
              <Field label={copy("backfillUntil")}><Input type="date" value={backfillRange.until} onChange={(event) => setBackfillRange((current) => ({ ...current, until: event.target.value }))} /></Field>
              <Button variant="outline" onClick={() => backfill.mutate()} disabled={!teamCurationRunnable || backfill.isPending || run.isPending || save.isPending || !backfillRange.since || !backfillRange.until} className="gap-2"><RefreshCw className={cn("h-4 w-4", backfill.isPending && "animate-spin")} />{copy("queueBackfill")}</Button>
            </div>
          </div>
        </div>
      </CardContent>
    </Card>
  );
}

function formatDateInput(date: Date): string {
  const year = date.getFullYear();
  const month = String(date.getMonth() + 1).padStart(2, "0");
  const day = String(date.getDate()).padStart(2, "0");
  return `${year}-${month}-${day}`;
}

function defaultBackfillUntil(): string {
  return formatDateInput(new Date());
}

function defaultBackfillSince(): string {
  const date = new Date();
  date.setDate(date.getDate() - 29);
  return formatDateInput(date);
}

function Field({ label, children }: { label: string; children: ReactNode }) {
  return <div className="space-y-1.5"><div className="text-xs text-muted-foreground">{label}</div>{children}</div>;
}

function MemoryCurationCard({
  status,
  loading,
  unavailable,
  onSelectRun,
}: {
  status: WorkspaceMemoryCurationStatus | undefined;
  loading: boolean;
  unavailable: boolean;
  onSelectRun: (runId: string) => void;
}) {
  const copy = useEvolutionCopy();
  const runs = new Map((status?.stages ?? []).map((run) => [run.stage, run] as const));
  const localPromotions = status?.local_proposals
    ?? runs.get("agent_self_review")?.stats.review_candidates_added
    ?? runs.get("all")?.stats.review_candidates_added
    ?? 0;
  const sharedCandidates = status?.pending_candidates ?? 0;
  const promotedSharedMemory = status?.team_knowledge_items ?? status?.promoted_candidates ?? 0;
  const pendingSkills = status?.pending_skills ?? 0;

  return (
    <Card className="bg-background/85 backdrop-blur">
      <CardHeader>
        <CardTitle className="flex items-center gap-2"><RefreshCw className={cn("h-4 w-4 text-brand", (status?.pending_runs ?? 0) > 0 && "animate-spin")} />{copy("memoryOps")}</CardTitle>
        <p className="text-sm text-muted-foreground">{copy("memoryOpsHint")}</p>
      </CardHeader>
      <CardContent className="space-y-3">
        <div className="grid grid-cols-3 gap-2">
          <MiniStat label={copy("localPromotion")} value={loading ? "…" : String(localPromotions)} />
          <MiniStat label={copy("sharedCandidates")} value={loading ? "…" : String(sharedCandidates)} />
          <MiniStat label={copy("sharedMemory")} value={loading ? "…" : String(promotedSharedMemory)} />
        </div>
        <div className="rounded-2xl border bg-muted/30 p-3 text-sm text-muted-foreground">
          <div>{copy("insight4")}</div>
          <div className="mt-2 text-xs">{copy("funnelHint").replace("{skills}", String(pendingSkills))}</div>
          <div className="mt-2 flex items-start gap-2 text-xs"><ShieldCheck className="mt-0.5 h-3.5 w-3.5 shrink-0 text-brand" />{copy("notBroadcast")}</div>
        </div>
        {MEMORY_CURATION_STAGES.map(([stageName, stageLabel, time, title, detail]) => {
          const run = runs.get(stageName);
          const duration = formatRunDuration(run);
          const stageMetric = stageName === "agent_self_review"
            ? `${run?.stats.review_candidates_added ?? 0} ${copy("candidatesAdded")} · ${run?.stats.evidence_collected ?? 0} ${copy("evidenceCollected")}`
            : `${run?.stats.shared_candidates_added ?? 0} ${copy("teamItems")} · ${run?.stats.conflicts_found ?? 0} ${copy("conflicts")}`;
          const isRunning = run?.status === "running" || run?.status === "queued";
          return (
            <button key={stageName} type="button" onClick={() => run?.id && onSelectRun(run.id)} className={cn("relative flex w-full items-center gap-3 overflow-hidden rounded-2xl border bg-muted/20 p-3 text-left transition-colors hover:border-brand/40", isRunning && "border-brand/40 bg-brand/5")}>
              {isRunning && <div className="absolute inset-y-0 left-0 w-1 animate-pulse bg-brand" />}
              <div className={cn("flex h-10 w-10 items-center justify-center rounded-full text-xs font-semibold", run ? "bg-foreground text-background" : "bg-muted text-muted-foreground", isRunning && "ring-4 ring-brand/15")}>{copy(stageLabel)}</div>
              <div className="min-w-0 flex-1">
                <div className="flex flex-wrap items-center gap-x-2 gap-y-1">
                  <span className="text-sm font-medium">{copy(title)}</span>
                  <span className="text-[11px] text-muted-foreground">{stageMetric}</span>
                </div>
                <div className="mt-0.5 text-xs text-muted-foreground">{time} {copy("beijingTime")} {"·"} {copy(detail)}</div>
                <div className="mt-1 text-[11px] text-muted-foreground">{run ? formatRunTime(run.finished_at ?? run.created_at, copy) : unavailable ? copy("unavailable") : copy("notRun")}{duration ? ` · ${duration}` : ""}</div>
                {run?.error && <div className="mt-1 line-clamp-2 text-[11px] text-destructive">{run.error}</div>}
              </div>
              <Badge variant={run?.status === "failed" ? "destructive" : isRunning ? "default" : run ? "secondary" : "outline"} className={cn(isRunning && "animate-pulse")}>{unavailable && !run ? copy("unavailable") : curationStatusLabel(run?.status, copy)}</Badge>
            </button>
          );
        })}
      </CardContent>
    </Card>
  );
}

function CurationRunDetailCard({ run, selectedRunId }: { run: MemoryCurationRunDetail | undefined; selectedRunId: string }) {
  const copy = useEvolutionCopy();
  if (!selectedRunId) {
    return <Card className="bg-background/85 backdrop-blur"><CardContent className="pt-6"><EmptyState text={copy("curationRunSelectHint")} /></CardContent></Card>;
  }
  if (!run || !run.id) {
    return <Card className="bg-background/85 backdrop-blur"><CardContent className="pt-6"><Skeleton className="h-32 rounded-2xl" /></CardContent></Card>;
  }
  return (
    <Card className="bg-background/85 backdrop-blur">
      <CardHeader>
        <CardTitle className="flex items-center gap-2"><Activity className="h-4 w-4 text-brand" />{copy("curationRunDetail")}</CardTitle>
        <p className="text-xs text-muted-foreground">{run.id}</p>
      </CardHeader>
      <CardContent className="space-y-4">
        <div className="grid gap-2 sm:grid-cols-2">
          <MiniStat label={copy("stage")} value={curationStageLabel(run.stage, copy)} />
          <MiniStat label={copy("stageStatus")} value={curationStatusLabel(run.status, copy)} />
          <MiniStat label={copy("runtime")} value={run.runtime_name || shortId(run.runtime_id)} />
          <MiniStat label={copy("curator")} value={run.curator_agent_name || shortId(run.curator_agent_id)} />
          <MiniStat label={copy("attempt")} value={run.attempt == null ? "-" : String(run.attempt)} />
          <MiniStat label={copy("claimAge")} value={run.claimed_at ? formatDuration(run.claimed_age_seconds ?? 0, "<1s") : "-"} />
          <MiniStat label={copy("runtimeLastSeen")} value={run.runtime_last_seen_at ? formatRunTime(run.runtime_last_seen_at, copy) : "-"} />
          <MiniStat label={copy("mode")} value={run.curator_mode || "-"} />
          <MiniStat label={copy("threshold")} value={run.confidence_threshold == null ? "-" : String(run.confidence_threshold)} />
        </div>
        {run.error && <div className="rounded-2xl border border-destructive/30 bg-destructive/10 p-3 text-sm text-destructive">{run.error}</div>}
        {run.diagnostics.length > 0 && (
          <div className="space-y-2">
            {run.diagnostics.map((item) => (
              <div key={`${item.code}:${item.message}`} className="rounded-2xl border bg-muted/25 p-3 text-sm">
                <div className="font-medium">{item.message}</div>
                {item.action && <div className="mt-1 text-xs text-muted-foreground">{copy("diagnosticAction")}: {item.action}</div>}
              </div>
            ))}
          </div>
        )}
        <div>
          <div className="mb-2 text-sm font-medium">{copy("targetAgents")}</div>
          <div className="flex flex-wrap gap-2">
            {run.target_agents.length === 0 ? <Badge variant="outline">{copy("noneRecorded")}</Badge> : run.target_agents.map((agent) => <Badge key={agent.id} variant="secondary">{agent.name || shortId(agent.id)}</Badge>)}
          </div>
        </div>
        {run.child_runs.length > 0 && (
          <div>
            <div className="mb-2 text-sm font-medium">{copy("selfReviewChildRuns")}</div>
            <div className="space-y-2">
              {run.child_runs.map((child) => (
                <div key={child.id} className="rounded-2xl border bg-card/70 p-3">
                  <div className="flex flex-wrap items-center justify-between gap-2">
                    <div className="font-medium">{child.agent_name || shortId(child.agent_id)}</div>
                    <Badge variant={child.status === "failed" ? "destructive" : child.status === "succeeded" ? "secondary" : "outline"}>{curationStatusLabel(child.status, copy)}</Badge>
                  </div>
                  <div className="mt-1 text-xs text-muted-foreground">{child.runtime_name || shortId(child.runtime_id)} · {copy("attempt")} {child.attempt}</div>
                  <div className="mt-3 grid grid-cols-4 gap-2">
                    <MiniStat label={copy("evidence")} value={String(child.evidence_collected)} />
                    <MiniStat label={copy("dailyReviewProposalFiles")} value={String(child.daily_files_written)} />
                    <MiniStat label={copy("memory")} value={String(child.review_candidates_added)} />
                    <MiniStat label={copy("skills")} value={String(child.skill_candidates_added)} />
                  </div>
                  {child.error && <div className="mt-2 text-xs text-destructive">{child.error}</div>}
                  {child.output_excerpt && <pre className="mt-3 max-h-40 overflow-auto rounded-xl bg-muted p-3 text-xs text-muted-foreground">{child.output_excerpt}</pre>}
                </div>
              ))}
            </div>
          </div>
        )}
        <div>
          <div className="mb-2 text-sm font-medium">{copy("timeline")}</div>
          <div className="space-y-2">
            {run.timeline.map((item, index) => (
              <div key={`${item.key}:${item.agent_id ?? "run"}:${item.timestamp ?? index}`} className="flex items-start gap-3 rounded-2xl border bg-muted/20 p-3">
                <Badge variant={item.status === "failed" ? "destructive" : item.status === "done" ? "secondary" : "outline"}>{curationTimelineStatusLabel(item.status, copy)}</Badge>
                <div className="min-w-0 flex-1">
                  <div className="text-sm font-medium">{item.label}</div>
                  <div className="mt-0.5 text-xs text-muted-foreground">{item.timestamp ? formatRunTime(item.timestamp, copy) : "-"}{item.detail ? ` · ${item.detail}` : ""}</div>
                </div>
              </div>
            ))}
          </div>
        </div>
        <div>
          <div className="mb-2 text-sm font-medium">{copy("perAgentResults")}</div>
          <div className="space-y-2">
            {run.agent_results.length === 0 ? <EmptyState text={copy("noPerAgentDetails")} /> : run.agent_results.map((agent) => (
              <div key={`${agent.agent_id}:${agent.root}`} className="rounded-2xl border bg-card/70 p-3">
                <div className="flex flex-wrap items-center justify-between gap-2">
                  <div className="font-medium">{agent.agent_name || shortId(agent.agent_id)}</div>
                  <Badge variant={agent.error ? "destructive" : agent.changed ? "secondary" : "outline"}>{agent.error ? copy("statusError") : agent.changed ? copy("statusChanged") : copy("statusUnchanged")}</Badge>
                </div>
                <div className="mt-1 truncate text-xs text-muted-foreground">{agent.root}</div>
                <div className="mt-3 grid grid-cols-3 gap-2">
                  <MiniStat label={copy("evidence")} value={String(agent.evidence_collected)} />
                  <MiniStat label={copy("memory")} value={String(agent.review_candidates_added)} />
                  <MiniStat label={copy("skills")} value={String(agent.skill_candidates_added)} />
                </div>
                {agent.error && <div className="mt-2 text-xs text-destructive">{agent.error}</div>}
                {agent.curator_output_excerpt && <pre className="mt-3 max-h-40 overflow-auto rounded-xl bg-muted p-3 text-xs text-muted-foreground">{agent.curator_output_excerpt}</pre>}
              </div>
            ))}
          </div>
        </div>
        {run.artifacts.length > 0 && (
          <div>
            <div className="mb-2 text-sm font-medium">{copy("artifacts")}</div>
            <div className="space-y-2">
              {run.artifacts.map((artifact, index) => (
                <div key={`${artifact.kind}:${artifact.agent_id ?? "team"}:${index}`} className="rounded-2xl border bg-muted/20 p-3 text-sm">
                  <div className="flex flex-wrap items-center gap-2"><Badge variant="outline">{artifact.kind}</Badge><span className="font-medium">{artifact.title}</span></div>
                  {artifact.detail && <div className="mt-1 text-xs text-muted-foreground">{artifact.detail}</div>}
                  {artifact.content && <pre className="mt-3 max-h-40 overflow-auto rounded-xl bg-background p-3 text-xs text-muted-foreground">{artifact.content}</pre>}
                </div>
              ))}
            </div>
          </div>
        )}
      </CardContent>
    </Card>
  );
}

function EvolutionTrendCard({ dailyMetrics }: { dailyMetrics: EvolutionDailyMetric[] }) {
  const copy = useEvolutionCopy();
  const recent = dailyMetrics.slice(-14);
  const maxValue = Math.max(1, ...recent.map((item) => item.memory_candidates + item.skill_candidates + item.promoted_memory + item.promoted_skill + (item.team_knowledge_items ?? 0)));
  const totals = dailyMetrics.reduce((acc, item) => ({
    memory: acc.memory + item.memory_candidates,
    skill: acc.skill + item.skill_candidates,
    promoted: acc.promoted + item.promoted_memory + item.promoted_skill,
    teamKnowledge: acc.teamKnowledge + (item.team_knowledge_items ?? 0),
    archived: acc.archived + item.archived_or_deprecated,
  }), { memory: 0, skill: 0, promoted: 0, teamKnowledge: 0, archived: 0 });
  return (
    <Card className="bg-background/85 backdrop-blur">
      <CardHeader>
        <CardTitle className="flex items-center gap-2"><TrendingUp className="h-4 w-4 text-emerald-500" />{copy("evolutionOutputTrend")}</CardTitle>
        <p className="text-sm text-muted-foreground">{copy("evolutionOutputTrendHint")}</p>
      </CardHeader>
      <CardContent className="space-y-4">
        <div className="grid grid-cols-2 gap-2 sm:grid-cols-5">
          <MiniStat label={copy("memory")} value={String(totals.memory)} />
          <MiniStat label="Skills" value={String(totals.skill)} />
          <MiniStat label="Promoted" value={String(totals.promoted)} />
          <MiniStat label={copy("teamKnowledgeShort")} value={String(totals.teamKnowledge)} />
          <MiniStat label="Archived" value={String(totals.archived)} />
        </div>
        <div className="flex h-36 items-end gap-1 rounded-2xl border bg-muted/20 p-3">
          {recent.length === 0 ? <EmptyState text={copy("noTrendData")} /> : recent.map((item) => {
            const total = item.memory_candidates + item.skill_candidates + item.promoted_memory + item.promoted_skill + (item.team_knowledge_items ?? 0);
            return <div key={item.date} className="flex min-w-0 flex-1 flex-col items-center gap-1"><Tooltip><TooltipTrigger render={<div className="w-full rounded-t bg-brand/70" style={{ height: `${Math.max(4, (total / maxValue) * 110)}px` }} />} /><TooltipContent side="top">{`${item.date}: ${total}`}</TooltipContent></Tooltip><div className="w-full truncate text-center text-[10px] text-muted-foreground">{item.date.slice(5)}</div></div>;
          })}
        </div>
      </CardContent>
    </Card>
  );
}

function TaskEfficiencyCard({ efficiency }: { efficiency: EvolutionTaskEfficiency | undefined }) {
  const copy = useEvolutionCopy();
  return (
    <Card className="bg-background/85 backdrop-blur">
      <CardHeader>
        <CardTitle className="flex items-center gap-2"><CircleDollarSign className="h-4 w-4 text-amber-500" />{copy("taskEfficiency")}</CardTitle>
        <p className="text-sm text-muted-foreground">{copy("taskEfficiencyHint")}</p>
      </CardHeader>
      <CardContent className="grid grid-cols-2 gap-2">
        <MiniStat label={copy("issues")} value={String(efficiency?.issue_count ?? 0)} />
        <MiniStat label={copy("avgDuration")} value={formatDuration(efficiency?.average_duration_seconds ?? 0, "<1s")} />
        <MiniStat label={copy("inputTokensShort")} value={compactNumber(efficiency?.average_input_tokens ?? 0)} />
        <MiniStat label={copy("outputTokensShort")} value={compactNumber(efficiency?.average_output_tokens ?? 0)} />
        <MiniStat label={copy("withLearnedUnits")} value={String(efficiency?.with_evolved_units_issue_count ?? 0)} />
        <MiniStat label={copy("avgUnitsUsed")} value={(efficiency?.average_evolved_units_used ?? 0).toFixed(1)} />
      </CardContent>
    </Card>
  );
}

function UnitMetricsCard({ metrics }: { metrics: EvolutionUnitMetric[] }) {
  const copy = useEvolutionCopy();
  const top = metrics.toSorted((a, b) => b.used_count - a.used_count || b.success_count - a.success_count).slice(0, 8);
  return (
    <Card className="bg-background/85 backdrop-blur">
      <CardHeader>
        <CardTitle className="flex items-center gap-2"><LineChart className="h-4 w-4 text-emerald-500" />{copy("successSignals")}</CardTitle>
        <p className="text-sm text-muted-foreground">{copy("insight3")}</p>
      </CardHeader>
      <CardContent className="space-y-3">
        {top.length === 0 ? <EmptyState text={copy("noCandidates")} /> : top.map((item) => (
          <div key={`${item.unit_type}:${item.unit_id ?? item.local_unit_id}`} className="rounded-2xl border bg-card/70 p-4">
            <div className="flex items-start justify-between gap-3">
              <div className="min-w-0">
                <div className="flex items-center gap-2">
                  <Badge variant="secondary">{unitLabel(item.unit_type, copy)}</Badge>
                  <Badge variant={item.success_rate >= 0.8 ? "secondary" : "outline"}>{pct(item.success_rate)}</Badge>
                </div>
                <div className="mt-2 truncate font-medium">{item.title || item.local_unit_id}</div>
              </div>
              <div className="text-right text-sm tabular-nums">
                <div className="font-semibold">{item.used_count}</div>
                <div className="text-xs text-muted-foreground">{copy("used")}</div>
              </div>
            </div>
            <div className="mt-3 grid grid-cols-3 gap-2">
              <MiniStat label={copy("successRate")} value={String(item.success_count)} />
              <MiniStat label={copy("failures")} value={String(item.failure_count)} />
              <MiniStat label={copy("attention")} value={String(item.conflict_count)} />
            </div>
          </div>
        ))}
      </CardContent>
    </Card>
  );
}

function OpsCard({ icon: Icon, title, value, detail, status }: { icon: typeof RefreshCw; title: string; value: string; detail: string; status: string }) {
  const copy = useEvolutionCopy();
  const badgeVariant: "secondary" | "default" | "destructive" | "outline" = status === copy("healthy") || status === copy("succeeded")
    ? "secondary"
    : status === copy("running")
      ? "default"
      : status === copy("attention") || status === copy("failed")
        ? "destructive"
        : "outline";
  return (
    <Card className="bg-background/85 backdrop-blur">
      <CardContent className="space-y-4 pt-1">
        <div className="flex items-start justify-between gap-3">
          <div className="rounded-2xl bg-brand/10 p-3 text-brand"><Icon className={cn("h-5 w-5", status === copy("running") && "animate-pulse")} /></div>
          <Badge variant={badgeVariant} className={cn(status === copy("running") && "animate-pulse")}>{status}</Badge>
        </div>
        <div>
          <div className="text-sm text-muted-foreground">{title}</div>
          <div className="mt-1 text-xl font-semibold">{value}</div>
          <div className="mt-1 text-xs text-muted-foreground">{detail}</div>
        </div>
      </CardContent>
    </Card>
  );
}

function ProcessCard({
  stageByName,
  sharedCandidates,
  promotedSharedMemory,
  feedbackCount,
}: {
  stageByName: Map<string, MemoryCurationStageStatus>;
  sharedCandidates: number;
  promotedSharedMemory: number;
  feedbackCount: number;
}) {
  const copy = useEvolutionCopy();
  const selfReview = stageByName.get("agent_self_review");
  const teamCuration = [stageByName.get("team_curation"), stageByName.get("all")]
    .filter((run): run is MemoryCurationStageStatus => run !== undefined)
    .toSorted((a, b) => Date.parse(b.created_at) - Date.parse(a.created_at))[0];
  const steps = [
    { label: "Active agents", detail: "Self-reviewed", value: selfReview?.stats.agents_scanned ?? 0, run: selfReview },
    { label: copy("candidates"), detail: "Agent proposals", value: selfReview?.stats.review_candidates_added ?? 0, run: selfReview },
    { label: copy("sharedPromotion"), detail: "Team curation items", value: teamCuration?.stats.shared_candidates_added ?? 0, run: teamCuration },
    { label: copy("attention"), detail: "Conflicts found", value: teamCuration?.stats.conflicts_found ?? 0, run: teamCuration },
    { label: copy("sharedCandidates"), detail: "Awaiting human review", value: sharedCandidates, run: teamCuration },
    { label: copy("workspaceUnit"), detail: copy("promotedSharedUnits"), value: promotedSharedMemory },
    { label: copy("feedback"), detail: "Observed uses", value: feedbackCount },
  ];
  return (
    <Card className="overflow-hidden bg-background/85 backdrop-blur lg:col-span-3">
      <CardHeader>
        <CardTitle className="flex items-center gap-2"><TrendingUp className="h-4 w-4 text-emerald-500" />{copy("curatorOps")}</CardTitle>
        <p className="text-sm text-muted-foreground">{copy("curatorOpsHint")}</p>
      </CardHeader>
      <CardContent>
        <div className="relative grid gap-3 md:grid-cols-3 xl:grid-cols-6">
          <div className="pointer-events-none absolute left-[8%] right-[8%] top-6 hidden h-px bg-gradient-to-r from-transparent via-brand/40 to-transparent xl:block" />
          {steps.map((step, index) => {
            const running = step.run?.status === "running" || step.run?.status === "queued";
            const active = step.value > 0 || running;
            return (
              <div key={step.label} className={cn("relative rounded-2xl border bg-muted/25 p-4 transition-colors", active && "border-brand/30 bg-brand/5")}>
                <div className={cn("relative z-10 flex h-9 w-9 items-center justify-center rounded-full border bg-background text-xs font-semibold", active && "border-brand/40 text-brand", running && "ring-4 ring-brand/15")}>
                  {running ? <Radio className="h-4 w-4 animate-pulse" /> : index + 1}
                </div>
                <div className="mt-5 text-2xl font-semibold tabular-nums">{step.value}</div>
                <div className="mt-1 text-sm font-medium">{step.label}</div>
                <div className="mt-1 text-xs text-muted-foreground">{step.detail}</div>
              </div>
            );
          })}
        </div>
      </CardContent>
    </Card>
  );
}

function InsightLine({ icon: Icon, text }: { icon: typeof CheckCircle2; text: string }) {
  return (
    <div className="flex items-start gap-2">
      <Icon className="mt-0.5 h-4 w-4 shrink-0 text-emerald-500" />
      <span>{text}</span>
    </div>
  );
}

function EmptyState({ text }: { text: string }) {
  return <div className="rounded-2xl border border-dashed p-6 text-center text-sm text-muted-foreground">{text}</div>;
}
