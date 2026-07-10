"use client";

import { useMemo, useState } from "react";
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
  Radio,
  RefreshCw,
  ShieldCheck,
  Sparkles,
  TrendingUp,
  WandSparkles,
} from "lucide-react";
import { useQuery } from "@tanstack/react-query";
import { useWorkspaceId } from "@multica/core/hooks";
import { useWorkspacePaths } from "@multica/core/paths";
import { agentListOptions } from "@multica/core/workspace/queries";
import {
  dashboardAgentRunTimeOptions,
  dashboardUsageByAgentOptions,
} from "@multica/core/dashboard";
import {
  evolutionMetricsOptions,
  evolutionReviewSubmissionListOptions,
  workspaceMemoryCurationStatusOptions,
} from "@multica/core/evolution";
import type {
  Agent,
  DashboardAgentRunTime,
  DashboardUsageByAgent,
  EvolutionReviewSubmission,
  EvolutionReviewSubmissionStatus,
  EvolutionUnitMetric,
  MemoryCurationStageStatus,
  WorkspaceMemoryCurationStatus,
} from "@multica/core/types";
import { Badge } from "@multica/ui/components/ui/badge";
import { buttonVariants } from "@multica/ui/components/ui/button";
import {
  Card,
  CardContent,
  CardHeader,
  CardTitle,
} from "@multica/ui/components/ui/card";
import { Progress } from "@multica/ui/components/ui/progress";
import { Skeleton } from "@multica/ui/components/ui/skeleton";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@multica/ui/components/ui/tabs";
import { cn } from "@multica/ui/lib/utils";
import { ActorAvatar } from "../../common/actor-avatar";
import { PageHeader } from "../../layout/page-header";
import { AppLink } from "../../navigation";
import {
  aggregateAgentTokens,
  formatDuration,
} from "../../dashboard/utils";

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
  agentTable: "Agents",
  agentColumn: "Agent",
  learningQueue: "Learning queue",
  learningQueueHint: "Review-first memory and skill candidates waiting for a human decision.",
  memoryOps: "Memory curation",
  memoryOpsHint: "Three organizer stages plus one daily curator, scheduled on Beijing time.",
  curatorOps: "Curator operations",
  curatorOpsHint: "Multi-agent curation, promotion, sharing, and safety checks.",
  starAgents: "Star agents",
  needsCoaching: "Needs coaching",
  memoryReview: "Memory review",
  skillDrafts: "Skill drafts",
  costEfficiency: "Cost efficiency",
  successRate: "Success rate",
  tasks: "Tasks",
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
  localPromotion: "Latest L3 local promotion",
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
  insight4: "L3 now writes local memory and opens workspace shared-memory proposals for review.",
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
};

const STATUSES = [
  "needs_review",
  "candidate",
  "promoted",
  "rejected",
] as const satisfies EvolutionReviewSubmissionStatus[];

const DAYS = 30;
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
const MEMORY_CURATION_STAGES = [
  ["l1_daily", "L1", "01:00", "Evidence intake", COPY.dbEvidence],
  ["l2_review", "L2", "02:00", "Candidate extraction", COPY.semanticDedupe],
  ["l3_promote", "L3", "03:00", "Local + shared promotion", COPY.sharedPromotion],
  ["l4_curator", "L4", "04:00", "Curator maintenance", COPY.curated],
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

function shortId(value: string | null | undefined): string {
  if (!value) return "—";
  return value.length <= 8 ? value : value.slice(0, 8);
}

function normalizeUnitType(value: string): "memory" | "skill" | "workflow" | "preference" | "other" {
  const lower = value.toLowerCase();
  if (lower.includes("skill")) return "skill";
  if (lower.includes("memory")) return "memory";
  if (lower.includes("preference")) return "preference";
  if (lower.includes("workflow") || lower.includes("tool")) return "workflow";
  return "other";
}

function isMemoryLikeUnitType(value: string): boolean {
  const normalized = normalizeUnitType(value);
  return normalized === "memory" || normalized === "preference" || normalized === "workflow";
}

function unitLabel(value: string): string {
  const normalized = normalizeUnitType(value);
  if (normalized === "memory") return COPY.memory;
  if (normalized === "skill") return COPY.skill;
  if (normalized === "workflow") return COPY.workflow;
  if (normalized === "preference") return COPY.preference;
  return value || COPY.all;
}

function statusLabel(value: string): string {
  if (value === "promoted") return COPY.promoted;
  if (value === "rejected") return COPY.rejected;
  if (value === "candidate") return COPY.candidates;
  return COPY.pending;
}

function curationStatusLabel(value: string | undefined): string {
  if (value === "running" || value === "queued") return COPY.running;
  if (value === "succeeded") return COPY.succeeded;
  if (value === "failed") return COPY.failed;
  return COPY.notRun;
}

function curationStageLabel(value: string): string {
  if (value === "all") return "ALL";
  return MEMORY_CURATION_STAGES.find(([stage]) => stage === value)?.[1] ?? value;
}

function formatRunTime(value: string | null | undefined): string {
  if (!value) return COPY.notRun;
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return COPY.notRun;
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
  const wsId = useWorkspaceId();
  const paths = useWorkspacePaths();
  const [learningFilter, setLearningFilter] = useState<"all" | "memory" | "skill">("all");

  const agentsQuery = useQuery(agentListOptions(wsId));
  const usageQuery = useQuery(dashboardUsageByAgentOptions(wsId, DAYS, null, VIEW_TZ));
  const runtimeQuery = useQuery(dashboardAgentRunTimeOptions(wsId, DAYS, null, VIEW_TZ));
  const needsReviewQuery = useQuery(evolutionReviewSubmissionListOptions(wsId, "needs_review"));
  const candidateQuery = useQuery(evolutionReviewSubmissionListOptions(wsId, "candidate"));
  const promotedQuery = useQuery(evolutionReviewSubmissionListOptions(wsId, "promoted"));
  const rejectedQuery = useQuery(evolutionReviewSubmissionListOptions(wsId, "rejected"));
  const { data: metricsData } = useQuery(evolutionMetricsOptions(wsId));
  const {
    data: curationStatus,
    isLoading: curationStatusLoading,
    isError: curationStatusUnavailable,
  } = useQuery(workspaceMemoryCurationStatusOptions(wsId));

  const agents = agentsQuery.data ?? EMPTY_AGENTS;
  const usageRows = usageQuery.data ?? EMPTY_USAGE_BY_AGENT;
  const runtimeRows = runtimeQuery.data ?? EMPTY_RUNTIME;
  const needsReviewSubmissions = needsReviewQuery.data ?? EMPTY_SUBMISSIONS;
  const candidateSubmissions = candidateQuery.data ?? EMPTY_SUBMISSIONS;
  const promotedSubmissions = promotedQuery.data ?? EMPTY_SUBMISSIONS;
  const rejectedSubmissions = rejectedQuery.data ?? EMPTY_SUBMISSIONS;
  const unitMetrics = metricsData?.unit_metrics ?? EMPTY_UNIT_METRICS;
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

  const loading = agentsQuery.isLoading || usageQuery.isLoading || runtimeQuery.isLoading;
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
  const sharedMemoryCandidates = memorySubmissions.filter(
    (item) => item.status === "candidate" || item.status === "needs_review",
  ).length;
  const promotedSharedMemory = memorySubmissions.filter((item) => item.status === "promoted").length;
  const curationHealth = curationStatusUnavailable
    ? COPY.unavailable
    : (curationStatus?.pending_runs ?? 0) > 0
      ? COPY.running
      : (curationStatus?.failed_runs_24h ?? 0) > 0
        ? COPY.attention
        : latestStage
          ? COPY.healthy
          : COPY.notRun;

  return (
    <div className="flex min-h-0 flex-1 flex-col bg-[radial-gradient(circle_at_top_left,hsl(var(--brand)/0.18),transparent_28rem),linear-gradient(135deg,hsl(var(--background)),hsl(var(--muted)/0.35))]">
      <PageHeader className="justify-between border-border/60 bg-background/70 backdrop-blur-xl">
        <div className="flex items-center gap-2">
          <Sparkles className="h-4 w-4 text-brand" />
          <h1 className="text-sm font-medium">{COPY.title}</h1>
          <Badge variant="secondary" className="hidden md:inline-flex">{COPY.liveSystem}</Badge>
        </div>
        <div className="hidden items-center gap-2 sm:flex">
          <AppLink href={paths.agents()} className={buttonVariants({ variant: "outline", size: "sm" })}>
            {COPY.openAgents}
          </AppLink>
          <AppLink href={paths.skills()} className={buttonVariants({ size: "sm", className: "gap-1.5" })}>
            {COPY.runReview}<ArrowUpRight className="h-3.5 w-3.5" />
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
                <Badge className="bg-white/15 text-white ring-1 ring-white/20">{COPY.eyebrow}</Badge>
                <div className="max-w-3xl space-y-3">
                  <h2 className="text-3xl font-semibold tracking-tight text-white md:text-5xl">{COPY.heroTitle}</h2>
                  <p className="max-w-2xl text-sm leading-6 text-white/72 md:text-base">{COPY.heroBody}</p>
                </div>
                <div className="grid max-w-3xl gap-3 sm:grid-cols-3">
                  <SignalPill icon={ShieldCheck} label={COPY.protected} value={COPY.review} />
                  <SignalPill icon={WandSparkles} label={COPY.autoDrafts} value={COPY.skillDrafts} />
                  <SignalPill icon={GitBranch} label={COPY.privateScope} value={COPY.curated} />
                </div>
              </div>
              <div className="grid content-end gap-3">
                <HeroMetric label={COPY.tasks} value={String(totals.taskCount)} detail={COPY.thirtyDays} />
                <HeroMetric label={COPY.successRate} value={pct(totals.successRate)} detail={`${totals.failedCount} ${COPY.failures.toLowerCase()}`} />
                <HeroMetric label={COPY.cost} value={money(totals.cost)} detail={`${totals.pending} ${COPY.pending.toLowerCase()}`} />
              </div>
            </div>
          </section>

          <div className="grid gap-4 md:grid-cols-2 xl:grid-cols-4">
            <MetricCard icon={Bot} label={COPY.starAgents} value={String(topAgents.length)} detail={topAgents[0]?.agent.display_name ?? topAgents[0]?.agent.name ?? COPY.noAgents} tone="emerald" />
            <MetricCard icon={BrainCircuit} label={COPY.memoryReview} value={String(totals.memoryItems)} detail={`${totals.pending} ${COPY.pending.toLowerCase()}`} tone="blue" />
            <MetricCard icon={Lightbulb} label={COPY.skillDrafts} value={String(totals.skillDrafts)} detail={COPY.autoDrafts} tone="amber" />
            <MetricCard icon={CircleDollarSign} label={COPY.costEfficiency} value={money(totals.cost / Math.max(1, totals.taskCount - totals.failedCount))} detail={COPY.costPerSuccess} tone="rose" />
          </div>

          <Tabs defaultValue="overview" className="gap-4">
            <TabsList className="w-full justify-start overflow-x-auto bg-background/70 p-1 shadow-sm backdrop-blur md:w-fit">
              <TabsTrigger value="overview" className="px-3">{COPY.tabOverview}</TabsTrigger>
              <TabsTrigger value="agents" className="px-3">{COPY.tabAgents}</TabsTrigger>
              <TabsTrigger value="learning" className="px-3">{COPY.tabLearning}</TabsTrigger>
              <TabsTrigger value="memory" className="px-3">{COPY.tabMemory}</TabsTrigger>
              <TabsTrigger value="ops" className="px-3">{COPY.tabOps}</TabsTrigger>
            </TabsList>

            <TabsContent value="overview" className="grid gap-4 md:grid-cols-2">
              <LearningPulseCard submissions={submissions} />
              <CoachingCard rows={coachingRows} />
            </TabsContent>

            <TabsContent value="agents" className="grid gap-4">
              <AgentTable rows={rows} loading={loading} />
            </TabsContent>

            <TabsContent value="learning" className="grid gap-4 xl:grid-cols-[.75fr_1.25fr]">
              <LearningSummaryCard totals={totals} />
              <LearningQueueCard
                submissions={filteredSubmissions}
                filter={learningFilter}
                onFilterChange={setLearningFilter}
              />
            </TabsContent>

            <TabsContent value="memory" className="grid gap-4 xl:grid-cols-[.8fr_1.2fr]">
              <MemoryCurationCard
                submissions={submissions}
                status={curationStatus}
                loading={curationStatusLoading}
                unavailable={curationStatusUnavailable}
              />
              <UnitMetricsCard metrics={unitMetrics} />
            </TabsContent>

            <TabsContent value="ops" className="grid gap-4 lg:grid-cols-3">
              <OpsCard
                icon={RefreshCw}
                title={COPY.lastRun}
                value={latestStage ? `${curationStageLabel(latestStage.stage)} · ${formatRunTime(latestStage.finished_at ?? latestStage.created_at)}` : COPY.notRun}
                detail={latestStage ? `${latestStage.stats.agents_scanned} ${COPY.agentsProcessed} · ${latestStage.stats.agents_changed} ${COPY.agentsChanged}` : COPY.memoryOpsHint}
                status={curationHealth}
              />
              <OpsCard
                icon={ShieldCheck}
                title={COPY.review}
                value={COPY.protected}
                detail={`${sharedMemoryCandidates} ${COPY.sharedCandidates.toLowerCase()} · ${curationStatus?.failed_runs_24h ?? 0} ${COPY.failures.toLowerCase()}`}
                status={(curationStatus?.failed_runs_24h ?? 0) > 0 ? COPY.attention : COPY.healthy}
              />
              <OpsCard icon={LineChart} title={COPY.projected} value={`${totals.memoryUsed}/${totals.skillUsed}`} detail={COPY.successSignals} status={totals.memoryUsed + totals.skillUsed > 0 ? COPY.healthy : COPY.attention} />
              <ProcessCard
                stageByName={stageByName}
                sharedCandidates={sharedMemoryCandidates}
                promotedSharedMemory={promotedSharedMemory}
                feedbackCount={totals.memoryUsed + totals.skillUsed}
              />
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
  const memory = submissions.filter((s) => isMemoryLikeUnitType(s.unit_type)).length;
  const skill = submissions.filter((s) => normalizeUnitType(s.unit_type) === "skill").length;
  const promoted = submissions.filter((s) => s.status === "promoted").length;
  const total = Math.max(1, submissions.length);
  return (
    <Card className="bg-background/85 backdrop-blur">
      <CardHeader>
        <CardTitle className="flex items-center gap-2"><BrainCircuit className="h-4 w-4 text-brand" />{COPY.learningQueue}</CardTitle>
        <p className="text-sm text-muted-foreground">{COPY.learningQueueHint}</p>
      </CardHeader>
      <CardContent className="space-y-4">
        <div className="grid grid-cols-3 gap-2">
          <MiniStat label={COPY.memory} value={String(memory)} />
          <MiniStat label={COPY.skill} value={String(skill)} />
          <MiniStat label={COPY.promoted} value={String(promoted)} />
        </div>
        <div className="space-y-2">
          <Progress value={(memory / total) * 100} className="h-2" />
          <Progress value={(skill / total) * 100} className="h-2 opacity-70" />
          <Progress value={(promoted / total) * 100} className="h-2 opacity-40" />
        </div>
        <div className="space-y-2 text-sm text-muted-foreground">
          <InsightLine icon={CheckCircle2} text={COPY.insight1} />
          <InsightLine icon={ShieldCheck} text={COPY.insight2} />
        </div>
      </CardContent>
    </Card>
  );
}

function CoachingCard({ rows }: { rows: AgentEvolutionRow[] }) {
  return (
    <Card className="bg-background/85 backdrop-blur">
      <CardHeader>
        <CardTitle className="flex items-center gap-2"><Activity className="h-4 w-4 text-rose-500" />{COPY.needsCoaching}</CardTitle>
      </CardHeader>
      <CardContent className="space-y-3">
        {rows.length === 0 ? <EmptyState text={COPY.noAgents} /> : rows.map((row) => (
          <div key={row.agent.id} className="flex items-center gap-3 rounded-2xl border bg-muted/20 p-3">
            <ActorAvatar actorType="agent" actorId={row.agent.id} size={30} showStatusDot />
            <div className="min-w-0 flex-1">
              <div className="truncate text-sm font-medium">{row.agent.display_name || row.agent.name}</div>
              <div className="text-xs text-muted-foreground">{row.failedCount} {COPY.failures.toLowerCase()} {"·"} {money(row.cost)} {COPY.cost.toLowerCase()}</div>
            </div>
            <Badge variant={row.failedCount > 0 ? "destructive" : "outline"}>{pct(row.successRate)}</Badge>
          </div>
        ))}
      </CardContent>
    </Card>
  );
}

function AgentTable({ rows, loading }: { rows: AgentEvolutionRow[]; loading: boolean }) {
  return (
    <Card className="bg-background/85 backdrop-blur">
      <CardHeader>
        <CardTitle>{COPY.agentTable}</CardTitle>
      </CardHeader>
      <CardContent className="overflow-x-auto">
        {loading ? <LeaderboardSkeleton /> : (
          <table className="w-full min-w-[920px] text-sm">
            <thead className="text-left text-xs uppercase tracking-wider text-muted-foreground">
              <tr className="border-b">
                <th className="pb-3 font-medium">{COPY.agentColumn}</th>
                <th className="pb-3 font-medium">{COPY.tasks}</th>
                <th className="pb-3 font-medium">{COPY.successRate}</th>
                <th className="pb-3 font-medium">{COPY.cost}</th>
                <th className="pb-3 font-medium">{COPY.costPerSuccess}</th>
                <th className="pb-3 font-medium">{COPY.learned}</th>
                <th className="pb-3 font-medium">{COPY.runtime}</th>
              </tr>
            </thead>
            <tbody>
              {rows.map((row) => (
                <tr key={row.agent.id} className="border-b last:border-0">
                  <td className="py-3">
                    <div className="flex items-center gap-3">
                      <ActorAvatar actorType="agent" actorId={row.agent.id} size={32} showStatusDot enableHoverCard />
                      <div>
                        <div className="font-medium">{row.agent.display_name || row.agent.name}</div>
                        <div className="text-xs text-muted-foreground">{shortId(row.agent.id)}</div>
                      </div>
                    </div>
                  </td>
                  <td className="py-3 tabular-nums">{row.taskCount}</td>
                  <td className="py-3"><Badge variant={row.successRate >= 0.8 ? "secondary" : "outline"}>{pct(row.successRate)}</Badge></td>
                  <td className="py-3 tabular-nums">{money(row.cost)}</td>
                  <td className="py-3 tabular-nums">{money(row.costPerSuccess)}</td>
                  <td className="py-3"><span className="tabular-nums">{row.learnedCount}</span> <span className="text-xs text-muted-foreground">{row.memoryCount}/{row.skillCount}</span></td>
                  <td className="py-3 tabular-nums">{formatDuration(row.seconds, "<1m")}</td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </CardContent>
    </Card>
  );
}

function LearningSummaryCard({ totals }: { totals: { pending: number; promoted: number; memoryItems: number; skillDrafts: number; learned: number } }) {
  return (
    <Card className="bg-background/85 backdrop-blur">
      <CardHeader>
        <CardTitle className="flex items-center gap-2"><Lightbulb className="h-4 w-4 text-amber-500" />{COPY.learningQueue}</CardTitle>
      </CardHeader>
      <CardContent className="space-y-4">
        <div className="grid grid-cols-2 gap-3">
          <MiniStat label={COPY.pending} value={String(totals.pending)} />
          <MiniStat label={COPY.promoted} value={String(totals.promoted)} />
          <MiniStat label={COPY.memory} value={String(totals.memoryItems)} />
          <MiniStat label={COPY.skill} value={String(totals.skillDrafts)} />
        </div>
        <div className="rounded-2xl border bg-muted/30 p-4 text-sm text-muted-foreground">
          {COPY.insight1}
        </div>
      </CardContent>
    </Card>
  );
}

function LearningQueueCard({ submissions, filter, onFilterChange }: { submissions: EvolutionReviewSubmission[]; filter: "all" | "memory" | "skill"; onFilterChange: (filter: "all" | "memory" | "skill") => void }) {
  return (
    <Card className="bg-background/85 backdrop-blur">
      <CardHeader>
        <div className="flex flex-col gap-3 sm:flex-row sm:items-start sm:justify-between">
          <div>
            <CardTitle className="flex items-center gap-2"><Sparkles className="h-4 w-4 text-brand" />{COPY.learningQueue}</CardTitle>
            <p className="mt-1 text-sm text-muted-foreground">{COPY.learningQueueHint}</p>
          </div>
          <div className="inline-flex rounded-lg bg-muted p-1">
            {(["all", "memory", "skill"] as const).map((value) => (
              <button key={value} type="button" onClick={() => onFilterChange(value)} className={cn("rounded-md px-3 py-1 text-xs font-medium", filter === value ? "bg-background shadow-sm" : "text-muted-foreground")}>{value === "all" ? COPY.all : value === "memory" ? COPY.memory : COPY.skill}</button>
            ))}
          </div>
        </div>
      </CardHeader>
      <CardContent className="space-y-3">
        {submissions.length === 0 ? <EmptyState text={COPY.noCandidates} /> : submissions.slice(0, 8).map((submission) => <SubmissionCard key={submission.id} submission={submission} />)}
      </CardContent>
    </Card>
  );
}

function SubmissionCard({ submission }: { submission: EvolutionReviewSubmission }) {
  return (
    <div className="rounded-2xl border bg-card/70 p-4 transition-colors hover:border-brand/30">
      <div className="flex flex-col gap-3 sm:flex-row sm:items-start sm:justify-between">
        <div className="min-w-0">
          <div className="flex flex-wrap items-center gap-2">
            <Badge variant="secondary">{unitLabel(submission.unit_type)}</Badge>
            <Badge variant={submission.status === "rejected" ? "destructive" : "outline"}>{statusLabel(submission.status)}</Badge>
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
        <span>{COPY.sensitivity}: {submission.sensitivity || "—"}</span>
        <span>{"·"}</span>
        <span>{COPY.source}: {submission.bundle_ref || shortId(submission.local_unit_id)}</span>
      </div>
    </div>
  );
}

function MemoryCurationCard({
  submissions,
  status,
  loading,
  unavailable,
}: {
  submissions: EvolutionReviewSubmission[];
  status: WorkspaceMemoryCurationStatus | undefined;
  loading: boolean;
  unavailable: boolean;
}) {
  const memorySubmissions = submissions.filter((item) => isMemoryLikeUnitType(item.unit_type));
  const sharedCandidates = memorySubmissions.filter((item) => item.status === "candidate" || item.status === "needs_review").length;
  const promotedSharedMemory = memorySubmissions.filter((item) => item.status === "promoted").length;
  const runs = new Map((status?.stages ?? []).map((run) => [run.stage, run] as const));
  const promotionRun = [runs.get("l3_promote"), runs.get("all")]
    .filter((run): run is MemoryCurationStageStatus => run !== undefined)
    .toSorted((a, b) => Date.parse(b.created_at) - Date.parse(a.created_at))[0];
  const localPromotions = promotionRun?.stats.entries_promoted ?? 0;

  return (
    <Card className="bg-background/85 backdrop-blur">
      <CardHeader>
        <CardTitle className="flex items-center gap-2"><RefreshCw className={cn("h-4 w-4 text-brand", (status?.pending_runs ?? 0) > 0 && "animate-spin")} />{COPY.memoryOps}</CardTitle>
        <p className="text-sm text-muted-foreground">{COPY.memoryOpsHint}</p>
      </CardHeader>
      <CardContent className="space-y-3">
        <div className="grid grid-cols-3 gap-2">
          <MiniStat label={COPY.localPromotion} value={loading ? "…" : String(localPromotions)} />
          <MiniStat label={COPY.sharedCandidates} value={String(sharedCandidates)} />
          <MiniStat label={COPY.sharedMemory} value={String(promotedSharedMemory)} />
        </div>
        <div className="rounded-2xl border bg-muted/30 p-3 text-sm text-muted-foreground">
          <div>{COPY.insight4}</div>
          <div className="mt-2 flex items-start gap-2 text-xs"><ShieldCheck className="mt-0.5 h-3.5 w-3.5 shrink-0 text-brand" />{COPY.notBroadcast}</div>
        </div>
        {MEMORY_CURATION_STAGES.map(([stageName, stageLabel, time, title, detail]) => {
          const run = runs.get(stageName);
          const duration = formatRunDuration(run);
          const stageMetric = stageName === "l1_daily"
            ? `${run?.stats.evidence_collected ?? 0} ${COPY.evidenceCollected}`
            : stageName === "l2_review"
              ? `${run?.stats.review_candidates_added ?? 0} ${COPY.candidatesAdded}`
              : stageName === "l3_promote"
                ? `${run?.stats.entries_promoted ?? 0} ${COPY.localPromotion.toLowerCase()} · ${run?.stats.shared_candidates_synced ?? 0} synced`
                : `${run?.stats.entries_archived ?? 0} ${COPY.archived} · ${run?.stats.duplicates_merged ?? 0} ${COPY.merged}`;
          const isRunning = run?.status === "running" || run?.status === "queued";
          return (
            <div key={stageName} className={cn("relative flex items-center gap-3 overflow-hidden rounded-2xl border bg-muted/20 p-3", isRunning && "border-brand/40 bg-brand/5")}>
              {isRunning && <div className="absolute inset-y-0 left-0 w-1 animate-pulse bg-brand" />}
              <div className={cn("flex h-10 w-10 items-center justify-center rounded-full text-xs font-semibold", run ? "bg-foreground text-background" : "bg-muted text-muted-foreground", isRunning && "ring-4 ring-brand/15")}>{stageLabel}</div>
              <div className="min-w-0 flex-1">
                <div className="flex flex-wrap items-center gap-x-2 gap-y-1">
                  <span className="text-sm font-medium">{title}</span>
                  <span className="text-[11px] text-muted-foreground">{stageMetric}</span>
                </div>
                <div className="mt-0.5 text-xs text-muted-foreground">{time} {COPY.beijingTime} {"·"} {detail}</div>
                <div className="mt-1 text-[11px] text-muted-foreground">{run ? formatRunTime(run.finished_at ?? run.created_at) : unavailable ? COPY.unavailable : COPY.notRun}{duration ? ` · ${duration}` : ""}</div>
              </div>
              <Badge variant={run?.status === "failed" ? "destructive" : isRunning ? "default" : run ? "secondary" : "outline"} className={cn(isRunning && "animate-pulse")}>{unavailable && !run ? COPY.unavailable : curationStatusLabel(run?.status)}</Badge>
            </div>
          );
        })}
      </CardContent>
    </Card>
  );
}

function UnitMetricsCard({ metrics }: { metrics: EvolutionUnitMetric[] }) {
  const top = metrics.toSorted((a, b) => b.used_count - a.used_count || b.success_count - a.success_count).slice(0, 8);
  return (
    <Card className="bg-background/85 backdrop-blur">
      <CardHeader>
        <CardTitle className="flex items-center gap-2"><LineChart className="h-4 w-4 text-emerald-500" />{COPY.successSignals}</CardTitle>
        <p className="text-sm text-muted-foreground">{COPY.insight3}</p>
      </CardHeader>
      <CardContent className="space-y-3">
        {top.length === 0 ? <EmptyState text={COPY.noCandidates} /> : top.map((item) => (
          <div key={`${item.unit_type}:${item.unit_id ?? item.local_unit_id}`} className="rounded-2xl border bg-card/70 p-4">
            <div className="flex items-start justify-between gap-3">
              <div className="min-w-0">
                <div className="flex items-center gap-2">
                  <Badge variant="secondary">{unitLabel(item.unit_type)}</Badge>
                  <Badge variant={item.success_rate >= 0.8 ? "secondary" : "outline"}>{pct(item.success_rate)}</Badge>
                </div>
                <div className="mt-2 truncate font-medium">{item.title || item.local_unit_id}</div>
              </div>
              <div className="text-right text-sm tabular-nums">
                <div className="font-semibold">{item.used_count}</div>
                <div className="text-xs text-muted-foreground">{COPY.used}</div>
              </div>
            </div>
            <div className="mt-3 grid grid-cols-3 gap-2">
              <MiniStat label={COPY.successRate} value={String(item.success_count)} />
              <MiniStat label={COPY.failures} value={String(item.failure_count)} />
              <MiniStat label={COPY.attention} value={String(item.conflict_count)} />
            </div>
          </div>
        ))}
      </CardContent>
    </Card>
  );
}

function OpsCard({ icon: Icon, title, value, detail, status }: { icon: typeof RefreshCw; title: string; value: string; detail: string; status: string }) {
  const badgeVariant: "secondary" | "default" | "destructive" | "outline" = status === COPY.healthy || status === COPY.succeeded
    ? "secondary"
    : status === COPY.running
      ? "default"
      : status === COPY.attention || status === COPY.failed
        ? "destructive"
        : "outline";
  return (
    <Card className="bg-background/85 backdrop-blur">
      <CardContent className="space-y-4 pt-1">
        <div className="flex items-start justify-between gap-3">
          <div className="rounded-2xl bg-brand/10 p-3 text-brand"><Icon className={cn("h-5 w-5", status === COPY.running && "animate-pulse")} /></div>
          <Badge variant={badgeVariant} className={cn(status === COPY.running && "animate-pulse")}>{status}</Badge>
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
  const l1 = stageByName.get("l1_daily");
  const l2 = stageByName.get("l2_review");
  const l3 = [stageByName.get("l3_promote"), stageByName.get("all")]
    .filter((run): run is MemoryCurationStageStatus => run !== undefined)
    .toSorted((a, b) => Date.parse(b.created_at) - Date.parse(a.created_at))[0];
  const steps = [
    { label: COPY.ingestion, detail: "Evidence collected", value: l1?.stats.evidence_collected ?? 0, run: l1 },
    { label: COPY.candidates, detail: "Review candidates", value: l2?.stats.review_candidates_added ?? 0, run: l2 },
    { label: COPY.localPromotion, detail: "Agent-private memory", value: l3?.stats.entries_promoted ?? 0, run: l3 },
    { label: COPY.sharedPromotion, detail: "Awaiting human review", value: sharedCandidates, run: l3 },
    { label: COPY.workspaceUnit, detail: "Promoted shared units", value: promotedSharedMemory },
    { label: COPY.feedback, detail: "Observed uses", value: feedbackCount },
  ];
  return (
    <Card className="overflow-hidden bg-background/85 backdrop-blur lg:col-span-3">
      <CardHeader>
        <CardTitle className="flex items-center gap-2"><TrendingUp className="h-4 w-4 text-emerald-500" />{COPY.curatorOps}</CardTitle>
        <p className="text-sm text-muted-foreground">{COPY.curatorOpsHint}</p>
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

function LeaderboardSkeleton() {
  return (
    <div className="space-y-3">
      {Array.from({ length: 4 }).map((_, index) => (
        <Skeleton key={index} className="h-24 rounded-2xl" />
      ))}
    </div>
  );
}
