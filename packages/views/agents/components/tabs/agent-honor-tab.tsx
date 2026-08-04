"use client";

import { useMemo, useReducer, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  Activity,
  Award,
  Check,
  Clock3,
  Gauge,
  LockKeyhole,
  Medal,
  Orbit,
  Settings2,
  ShieldCheck,
  Star,
  Trophy,
  Zap,
} from "lucide-react";
import { toast } from "sonner";
import type {
  Agent,
  AgentAchievement,
  AgentHonorRules,
  AgentHonorRulesView,
} from "@multica/core/types";
import {
  agentHonorAuditOptions,
  agentHonorKeys,
  agentHonorOptions,
  agentHonorRulesOptions,
} from "@multica/core/agents";
import { api } from "@multica/core/api";
import { useWorkspaceId } from "@multica/core/hooks";
import { FleetRankBadge } from "@multica/ui/components/fleet/fleet-class-badge";
import { Button } from "@multica/ui/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from "@multica/ui/components/ui/dialog";
import { Input } from "@multica/ui/components/ui/input";
import { Switch } from "@multica/ui/components/ui/switch";
import { showErrorToast } from "@multica/ui/lib/error-toast";
import { cn } from "@multica/ui/lib/utils";
import { useT, useTimeAgo } from "../../../i18n";
import { AgentHonorAchievementIcon } from "../agent-honor-achievement-icon";
import {
  useAgentAchievementCategoryName,
  useAgentAchievementCopy,
} from "../../hooks/use-agent-achievement-copy";
import { useAgentFleetClassName } from "../../hooks/use-agent-fleet-class-name";
import { useAgentHonorCopy } from "../../hooks/use-agent-honor-copy";
import {
  AgentHonorLevelIcon,
  MAX_AGENT_HONOR_LEVEL,
} from "../agent-honor-level-icon";

const fleetPillars = ["delivery", "evolution", "growth", "efficiency"] as const;

type AgentHonorAdminState = {
  rules: AgentHonorRules;
  grantKind: "xp" | "achievement";
  grantXP: number;
  achievementId: string;
  reason: string;
};

type AgentHonorAdminAction =
  | { type: "update_rules"; update: (current: AgentHonorRules) => AgentHonorRules }
  | { type: "set_grant_kind"; value: "xp" | "achievement" }
  | { type: "set_grant_xp"; value: number }
  | { type: "set_achievement_id"; value: string }
  | { type: "set_reason"; value: string };

function createAgentHonorAdminState(rulesView: AgentHonorRulesView): AgentHonorAdminState {
  return {
    rules: {
      ...rulesView.rules,
      fleet_weights: { ...rulesView.rules.fleet_weights },
      fleet_classes: rulesView.rules.fleet_classes.map((item) => ({ ...item })),
      achievement_targets: { ...rulesView.rules.achievement_targets },
      achievement_enabled: { ...rulesView.rules.achievement_enabled },
      changelog: [...rulesView.rules.changelog],
    },
    grantKind: "xp",
    grantXP: 25,
    achievementId: rulesView.achievements[0]?.id ?? "",
    reason: "",
  };
}

function agentHonorAdminReducer(
  state: AgentHonorAdminState,
  action: AgentHonorAdminAction,
): AgentHonorAdminState {
  switch (action.type) {
    case "update_rules":
      return { ...state, rules: action.update(state.rules) };
    case "set_grant_kind":
      return { ...state, grantKind: action.value };
    case "set_grant_xp":
      return { ...state, grantXP: action.value };
    case "set_achievement_id":
      return { ...state, achievementId: action.value };
    case "set_reason":
      return { ...state, reason: action.value };
  }
}

function percent(value: number) {
  return `${Math.round(Math.max(0, Math.min(1, value)) * 100)}%`;
}

function progressPercent(current: number, target: number) {
  if (target <= 0) return 100;
  return Math.max(0, Math.min(100, (current / target) * 100));
}

function Panel({
  className,
  children,
}: {
  className?: string;
  children: React.ReactNode;
}) {
  return (
    <section className={cn("rounded-2xl border border-border/60 bg-card p-4", className)}>
      {children}
    </section>
  );
}

function PanelTitle({
  icon: Icon,
  title,
  hint,
}: {
  icon: typeof Trophy;
  title: string;
  hint?: string;
}) {
  return (
    <div className="mb-4 flex items-start gap-2.5">
      <span className="grid size-8 shrink-0 place-items-center rounded-lg bg-primary/10 text-primary">
        <Icon className="size-4" />
      </span>
      <div>
        <h3 className="text-sm font-semibold text-foreground">{title}</h3>
        {hint ? <p className="mt-0.5 text-xs text-muted-foreground">{hint}</p> : null}
      </div>
    </div>
  );
}

export function AchievementCard({
  achievement,
  selected,
  equipped,
  editable,
  onToggle,
  onEquip,
}: {
  achievement: AgentAchievement;
  selected: boolean;
  equipped: boolean;
  editable: boolean;
  onToggle: () => void;
  onEquip: () => void;
}) {
  const { t } = useT("agents");
  const achievementCopy = useAgentAchievementCopy();
  const copy = achievementCopy(achievement);
  const progress = achievement.progress
    ? progressPercent(achievement.progress.current, achievement.progress.target)
    : achievement.unlocked
      ? 100
      : 0;

  return (
    <article
      className={cn(
        "group relative flex min-h-48 flex-col rounded-xl border p-3 transition",
        achievement.unlocked
          ? "border-border/70 bg-background/80 hover:border-primary/40"
          : "border-border/40 bg-muted/25",
        selected && "border-cyan-400/60 bg-cyan-500/[0.05] shadow-[0_0_24px_-16px_rgba(34,211,238,0.8)]",
        equipped && "ring-1 ring-violet-400/60",
      )}
    >
      <div className="flex items-start justify-between gap-2">
        <AgentHonorAchievementIcon
          rarity={achievement.rarity}
          title={copy.title}
          locked={!achievement.unlocked}
          featured={equipped || selected}
          className="size-14"
        />
        <div className="flex flex-col items-end gap-1">
          <span className="rounded-full bg-muted px-2 py-0.5 text-[10px] text-muted-foreground">
            {copy.category}
          </span>
          {achievement.unlocked ? (
            <span className="inline-flex items-center gap-1 text-[10px] font-medium text-emerald-600 dark:text-emerald-300">
              <Check className="size-3" />
              {t(($) => $.honor_agent.unlocked)}
            </span>
          ) : (
            <LockKeyhole className="size-3.5 text-muted-foreground" />
          )}
        </div>
      </div>
      <h4 className="mt-3 text-sm font-semibold text-foreground">{copy.title}</h4>
      <p className="mt-1 line-clamp-2 text-xs leading-5 text-muted-foreground">
        {copy.description}
      </p>
      <div className="mt-auto pt-3">
        {achievement.progress ? (
          <>
            <div className="mb-1.5 flex items-center justify-between text-[10px] text-muted-foreground">
              <span>
                {achievement.progress.current}/{achievement.progress.target}
              </span>
              <span>
                {t(($) => $.honor_agent.xp_value, {
                  value: `+${achievement.xp_reward}`,
                })}
              </span>
            </div>
            <div className="h-1.5 overflow-hidden rounded-full bg-muted">
              <div
                className="h-full rounded-full bg-gradient-to-r from-cyan-400 via-violet-500 to-fuchsia-500"
                style={{ width: `${progress}%` }}
              />
            </div>
          </>
        ) : (
          <div className="text-[10px] text-muted-foreground">
            {t(($) => $.honor_agent.xp_value, {
              value: `+${achievement.xp_reward}`,
            })}
          </div>
        )}
        {editable && achievement.unlocked ? (
          <div className="mt-2 flex gap-1.5">
            <Button
              type="button"
              size="sm"
              variant={selected ? "secondary" : "outline"}
              className="h-7 flex-1 text-[11px]"
              onClick={onToggle}
            >
              {selected
                ? t(($) => $.honor_agent.remove_showcase)
                : t(($) => $.honor_agent.add_showcase)}
            </Button>
            <Button
              type="button"
              size="sm"
              variant={equipped ? "secondary" : "ghost"}
              className="h-7 px-2 text-[11px]"
              onClick={onEquip}
            >
              {equipped ? t(($) => $.honor_agent.equipped) : t(($) => $.honor_agent.equip)}
            </Button>
          </div>
        ) : null}
      </div>
    </article>
  );
}

export function AgentHonorTab({
  agent,
  canManage,
}: {
  agent: Agent;
  canManage: boolean;
}) {
  const { t } = useT("agents");
  const achievementCopy = useAgentAchievementCopy();
  const achievementCategoryName = useAgentAchievementCategoryName();
  const fleetClassName = useAgentFleetClassName();
  const honorCopy = useAgentHonorCopy();
  const timeAgo = useTimeAgo();
  const workspaceId = useWorkspaceId();
  const queryClient = useQueryClient();
  const {
    data: dashboard,
    isLoading: isHonorLoading,
    isError: isHonorError,
    refetch: refetchHonor,
  } = useQuery(agentHonorOptions(workspaceId, agent.id));
  const {
    data: rulesView,
    isLoading: areRulesLoading,
    isError: areRulesError,
    refetch: refetchRules,
  } = useQuery(agentHonorRulesOptions(workspaceId));
  const { data: audit = [] } = useQuery({
    ...agentHonorAuditOptions(workspaceId, agent.id),
    enabled: canManage,
  });
  const [category, setCategory] = useState("all");

  const updateShowcase = useMutation({
    mutationFn: (input: { achievement_ids: string[]; equipped_id: string }) =>
      api.updateAgentHonorShowcase(agent.id, input),
    onSuccess: (dashboard) => {
      queryClient.setQueryData(agentHonorKeys.dashboard(workspaceId, agent.id), dashboard);
    },
    onError: () => showErrorToast(t(($) => $.honor_agent.update_error)),
  });

  if (isHonorLoading || areRulesLoading) {
    return (
      <div className="grid gap-4 p-4 md:p-6">
        <div className="h-64 animate-pulse rounded-2xl bg-muted" />
        <div className="grid grid-cols-2 gap-3 lg:grid-cols-4">
          {Array.from({ length: 8 }, (_, index) => (
            <div key={index} className="h-48 animate-pulse rounded-xl bg-muted" />
          ))}
        </div>
      </div>
    );
  }

  if (!dashboard || !rulesView || isHonorError || areRulesError) {
    return (
      <div className="grid min-h-80 place-items-center p-6 text-center">
        <div>
          <p className="text-sm font-medium">{t(($) => $.honor_agent.load_error)}</p>
          <Button
            className="mt-3"
            size="sm"
            variant="outline"
            onClick={() => {
              void refetchHonor();
              void refetchRules();
            }}
          >
            {t(($) => $.honor_agent.retry)}
          </Button>
        </div>
      </div>
    );
  }

  const categories = [
    "all",
    ...Array.from(new Set(dashboard.achievements.map((item) => item.category))),
  ];
  const visibleAchievements =
    category === "all"
      ? dashboard.achievements
      : dashboard.achievements.filter((item) => item.category === category);
  const nextTargets = dashboard.achievements
    .filter((item) => !item.unlocked && item.progress)
    .sort((left, right) => {
      const leftPct = progressPercent(left.progress?.current ?? 0, left.progress?.target ?? 1);
      const rightPct = progressPercent(right.progress?.current ?? 0, right.progress?.target ?? 1);
      return rightPct - leftPct;
    })
    .slice(0, 3);
  const unlockedCount = dashboard.achievements.filter((item) => item.unlocked).length;
  const levelStart = dashboard.level <= 1 ? 0 : 25 * (dashboard.level - 1) ** 2;
  const levelEnd =
    dashboard.level >= MAX_AGENT_HONOR_LEVEL
      ? dashboard.total_xp
      : 25 * dashboard.level ** 2;
  const levelProgress =
    dashboard.level >= MAX_AGENT_HONOR_LEVEL
      ? 100
      : progressPercent(dashboard.total_xp - levelStart, levelEnd - levelStart);
  const equipped =
    dashboard.achievements.find((item) => item.id === dashboard.equipped_achievement_id) ??
    null;
  const equippedTitle = equipped ? achievementCopy(equipped).title : "";

  const setShowcase = (achievement: AgentAchievement) => {
    const selected = dashboard.showcase_achievement_ids.includes(achievement.id);
    const next = selected
      ? dashboard.showcase_achievement_ids.filter((id) => id !== achievement.id)
      : [...dashboard.showcase_achievement_ids, achievement.id].slice(-3);
    const equippedId =
      dashboard.equipped_achievement_id === achievement.id && selected
        ? ""
        : (dashboard.equipped_achievement_id ?? "");
    updateShowcase.mutate({ achievement_ids: next, equipped_id: equippedId });
  };

  const setEquipped = (achievement: AgentAchievement) => {
    const equippedId =
      dashboard.equipped_achievement_id === achievement.id ? "" : achievement.id;
    const showcase = dashboard.showcase_achievement_ids.includes(achievement.id)
      ? dashboard.showcase_achievement_ids
      : [...dashboard.showcase_achievement_ids, achievement.id].slice(-3);
    updateShowcase.mutate({ achievement_ids: showcase, equipped_id: equippedId });
  };

  return (
    <div className="flex flex-col gap-4 p-4 md:p-6">
      <section className="relative isolate min-h-64 overflow-hidden rounded-2xl border border-cyan-300/20 bg-[#030817] p-5 text-white shadow-[0_24px_80px_-48px_rgba(34,211,238,0.7)] md:p-7">
        <div className="absolute inset-0 -z-20 bg-[radial-gradient(circle_at_82%_26%,rgba(99,102,241,0.28),transparent_28%),radial-gradient(circle_at_28%_92%,rgba(6,182,212,0.2),transparent_35%),linear-gradient(120deg,#020617_0%,#080b27_52%,#10133a_100%)]" />
        <div className="absolute -right-16 -top-24 -z-10 size-80 rounded-full border border-cyan-200/10 shadow-[0_0_80px_rgba(34,211,238,0.12),inset_0_0_70px_rgba(99,102,241,0.1)]" />
        <div className="absolute right-16 top-6 -z-10 size-40 rounded-full border border-violet-300/15" />
        <div className="flex flex-col justify-between gap-8 md:flex-row">
          <div className="max-w-xl">
            <div className="inline-flex items-center gap-2 rounded-full border border-cyan-200/20 bg-cyan-200/[0.06] px-3 py-1 text-[10px] uppercase tracking-[0.22em] text-cyan-100">
              <Orbit className="size-3" />
              {t(($) => $.honor_agent.eyebrow)}
            </div>
            <h2 className="mt-4 text-2xl font-semibold tracking-tight md:text-3xl">
              {t(($) => $.honor_agent.title)}
            </h2>
            <p className="mt-2 max-w-lg text-sm leading-6 text-slate-300">
              {t(($) => $.honor_agent.subtitle)}
            </p>
            <div className="mt-5 flex flex-wrap items-center gap-2.5">
              <span className="rounded-lg border border-white/15 bg-white/[0.06] px-3 py-1.5 text-sm font-semibold">
                {agent.name}
              </span>
              <span className="rounded-full border border-violet-300/25 bg-violet-400/10 px-3 py-1 text-xs text-violet-100">
                {t(($) => $.honor_agent.level_value, { level: dashboard.level })}
              </span>
              <FleetRankBadge
                classId={dashboard.fleet.class_id}
                classLabel={fleetClassName(
                  dashboard.fleet.class_id,
                  dashboard.fleet.class_label,
                )}
                fleetRank={dashboard.fleet.fleet_rank}
                frozen={dashboard.fleet.frozen}
              />
            </div>
          </div>
          <div className="flex items-center gap-4 self-start rounded-2xl border border-white/10 bg-white/[0.04] p-4 backdrop-blur-sm">
            <AgentHonorLevelIcon
              level={dashboard.level}
              title={t(($) => $.honor_agent.level_value, { level: dashboard.level })}
              className="size-24 drop-shadow-[0_0_20px_rgba(99,102,241,0.35)]"
              priority
            />
            <div className="min-w-0">
              <p className="text-[10px] uppercase tracking-widest text-cyan-200/70">
                {t(($) => $.honor_agent.level_value, { level: dashboard.level })}
              </p>
              <div className="mt-3 flex items-center gap-2.5">
                {equipped ? (
                  <AgentHonorAchievementIcon
                    rarity={equipped.rarity}
                    title={equippedTitle}
                    featured
                    className="size-10"
                  />
                ) : (
                  <span className="grid size-10 shrink-0 place-items-center rounded-lg border border-dashed border-white/20 text-slate-400">
                    <Award className="size-4" />
                  </span>
                )}
                <div className="min-w-0">
                  <p className="text-[10px] uppercase tracking-widest text-slate-400">
                    {t(($) => $.honor_agent.equipped)}
                  </p>
                  <p className="mt-0.5 max-w-32 truncate text-sm font-semibold">
                    {equippedTitle || t(($) => $.honor_agent.no_equipped)}
                  </p>
                </div>
              </div>
            </div>
          </div>
        </div>
        <div className="mt-8 grid gap-4 md:grid-cols-[auto_1fr] md:items-end">
          <div>
            <div className="text-4xl font-semibold tabular-nums">
              {dashboard.total_xp}
              <span className="ml-1 text-sm font-medium text-cyan-300">
                {t(($) => $.honor_agent.xp_label)}
              </span>
            </div>
            <p className="mt-1 text-xs text-slate-400">
              {unlockedCount}/{dashboard.achievements.length}{" "}
              {t(($) => $.honor_agent.achievements_unlocked)}
            </p>
          </div>
          <div>
            <div className="mb-1.5 flex justify-between text-[10px] text-slate-400">
              <span>{t(($) => $.honor_agent.next_level)}</span>
              <span>
                {dashboard.xp_to_next_level > 0
                  ? t(($) => $.honor_agent.xp_value, {
                      value: `+${dashboard.xp_to_next_level}`,
                    })
                  : t(($) => $.honor_agent.max_level)}
              </span>
            </div>
            <div className="h-2 overflow-hidden rounded-full bg-white/10">
              <div
                className="h-full rounded-full bg-gradient-to-r from-cyan-400 via-violet-500 to-fuchsia-500 shadow-[0_0_18px_rgba(99,102,241,0.8)]"
                style={{ width: `${levelProgress}%` }}
              />
            </div>
          </div>
        </div>
        {canManage ? (
          <div className="absolute right-4 top-4">
            <AgentHonorAdminDialog
              agent={agent}
              rulesView={rulesView}
              audit={audit}
            />
          </div>
        ) : null}
      </section>

      <div className="grid gap-4 xl:grid-cols-[minmax(0,1.5fr)_minmax(280px,0.7fr)]">
        <Panel>
          <PanelTitle
            icon={Zap}
            title={t(($) => $.honor_agent.next_targets)}
            hint={t(($) => $.honor_agent.next_targets_hint)}
          />
          <div className="grid gap-2 md:grid-cols-3">
            {nextTargets.length > 0 ? (
              nextTargets.map((item) => {
                const copy = achievementCopy(item);
                return (
                  <div
                    key={item.id}
                    className="rounded-xl border border-border/60 bg-muted/20 p-3"
                  >
                    <div className="flex items-center gap-2">
                      <AgentHonorAchievementIcon
                        rarity={item.rarity}
                        title={copy.title}
                        locked
                        className="size-10"
                      />
                      <div className="min-w-0">
                        <p className="truncate text-xs font-semibold text-foreground">
                          {copy.title}
                        </p>
                        <p className="text-[10px] text-muted-foreground">
                          {t(($) => $.honor_agent.xp_value, {
                            value: `+${item.xp_reward}`,
                          })}
                        </p>
                      </div>
                    </div>
                    {item.progress ? (
                      <>
                        <div className="mt-3 h-1.5 overflow-hidden rounded-full bg-muted">
                          <div
                            className="h-full rounded-full bg-primary"
                            style={{
                              width: `${progressPercent(item.progress.current, item.progress.target)}%`,
                            }}
                          />
                        </div>
                        <p className="mt-1 text-right text-[10px] tabular-nums text-muted-foreground">
                          {item.progress.current}/{item.progress.target}
                        </p>
                      </>
                    ) : null}
                  </div>
                );
              })
            ) : (
              <p className="text-xs text-muted-foreground">
                {t(($) => $.honor_agent.all_unlocked)}
              </p>
            )}
          </div>
        </Panel>

        <Panel>
          <PanelTitle
            icon={Gauge}
            title={t(($) => $.honor_agent.fleet_progress)}
            hint={
              dashboard.next_fleet_class
                ? `${dashboard.fleet.fleet_score.toFixed(1)} / ${dashboard.next_fleet_class.score}`
                : t(($) => $.honor_agent.top_class)
            }
          />
          <div className="grid grid-cols-2 gap-2">
            {fleetPillars.map((pillar) => (
              <div key={pillar} className="rounded-lg border border-border/50 bg-muted/20 p-2.5">
                <p className="text-[10px] text-muted-foreground">
                  {t(($) => $.fleet.pillars[pillar])}
                </p>
                <p className="mt-1 text-sm font-semibold tabular-nums">
                  {percent(dashboard.fleet.pillars[pillar])}
                </p>
              </div>
            ))}
          </div>
        </Panel>
      </div>

      <Panel>
        <PanelTitle
          icon={Medal}
          title={t(($) => $.honor_agent.showcase)}
          hint={t(($) => $.honor_agent.showcase_hint)}
        />
        <div className="grid gap-3 sm:grid-cols-3">
          {Array.from({ length: 3 }, (_, index) => {
            const achievement = dashboard.achievements.find(
              (item) => item.id === dashboard.showcase_achievement_ids[index],
            );
            const copy = achievement ? achievementCopy(achievement) : null;
            return (
              <div
                key={index}
                className="flex min-h-28 items-center gap-3 rounded-xl border border-dashed border-border/70 bg-muted/15 p-3"
              >
                {achievement ? (
                  <>
                    <AgentHonorAchievementIcon
                      rarity={achievement.rarity}
                      title={copy?.title ?? achievement.title}
                      featured
                      className="size-14"
                    />
                    <div>
                      <p className="text-xs font-semibold text-foreground">
                        {copy?.title ?? achievement.title}
                      </p>
                      <p className="mt-1 text-[10px] text-muted-foreground">
                        {achievement.rarity}% {t(($) => $.honor_agent.rarity)}
                      </p>
                    </div>
                  </>
                ) : (
                  <div className="mx-auto text-center text-muted-foreground">
                    <Star className="mx-auto size-5 opacity-40" />
                    <p className="mt-1 text-[10px]">
                      {t(($) => $.honor_agent.empty_showcase)}
                    </p>
                  </div>
                )}
              </div>
            );
          })}
        </div>
      </Panel>

      <Panel>
        <div className="mb-4 flex flex-col justify-between gap-3 sm:flex-row sm:items-start">
          <PanelTitle
            icon={Trophy}
            title={t(($) => $.honor_agent.collection)}
            hint={t(($) => $.honor_agent.collection_hint)}
          />
          <div className="flex max-w-full gap-1 overflow-x-auto">
            {categories.map((item) => (
              <button
                key={item}
                type="button"
                onClick={() => setCategory(item)}
                className={cn(
                  "shrink-0 rounded-full px-2.5 py-1 text-[11px] transition",
                  category === item
                    ? "bg-foreground text-background"
                    : "bg-muted text-muted-foreground hover:text-foreground",
                )}
              >
                {item === "all"
                  ? t(($) => $.honor_agent.all)
                  : achievementCategoryName(item)}
              </button>
            ))}
          </div>
        </div>
        <div className="grid grid-cols-1 gap-3 sm:grid-cols-2 xl:grid-cols-4">
          {visibleAchievements.map((achievement) => (
            <AchievementCard
              key={achievement.id}
              achievement={achievement}
              selected={dashboard.showcase_achievement_ids.includes(achievement.id)}
              equipped={dashboard.equipped_achievement_id === achievement.id}
              editable={canManage && !updateShowcase.isPending}
              onToggle={() => setShowcase(achievement)}
              onEquip={() => setEquipped(achievement)}
            />
          ))}
        </div>
      </Panel>

      <div className="grid gap-4 lg:grid-cols-2">
        <Panel>
          <PanelTitle
            icon={Activity}
            title={t(($) => $.honor_agent.recent_xp)}
            hint={t(($) => $.honor_agent.recent_xp_hint)}
          />
          <div className="max-h-72 space-y-1 overflow-y-auto">
            {dashboard.recent_events.length > 0 ? (
              dashboard.recent_events.map((event) => (
                <div
                  key={event.id}
                  className="flex items-center gap-3 rounded-lg px-2 py-2 hover:bg-muted/40"
                >
                  <span
                    className={cn(
                      "grid size-8 shrink-0 place-items-center rounded-lg",
                      event.xp_delta >= 0
                        ? "bg-emerald-500/10 text-emerald-600 dark:text-emerald-300"
                        : "bg-destructive/10 text-destructive",
                    )}
                  >
                    {event.event_type === "achievement" ? (
                      <Award className="size-4" />
                    ) : (
                      <Zap className="size-4" />
                    )}
                  </span>
                  <div className="min-w-0 flex-1">
                    <p className="truncate text-xs font-medium text-foreground">
                      {honorCopy.eventReason(event)}
                    </p>
                    <p className="text-[10px] text-muted-foreground">
                      {timeAgo(event.created_at)}
                    </p>
                  </div>
                  <span className="text-xs font-semibold tabular-nums">
                    {t(($) => $.honor_agent.xp_value, {
                      value: `${event.xp_delta > 0 ? "+" : ""}${event.xp_delta}`,
                    })}
                  </span>
                </div>
              ))
            ) : (
              <p className="text-xs text-muted-foreground">
                {t(($) => $.honor_agent.no_activity)}
              </p>
            )}
          </div>
        </Panel>

        <Panel>
          <PanelTitle
            icon={Clock3}
            title={t(($) => $.honor_agent.fleet_history)}
            hint={t(($) => $.honor_agent.fleet_history_hint)}
          />
          <div className="max-h-72 space-y-2 overflow-y-auto">
            {dashboard.fleet_history.length > 0 ? (
              dashboard.fleet_history.map((entry, index) => (
                <div key={`${entry.recorded_at}-${index}`} className="flex items-center gap-3">
                  <span className="relative flex size-3 shrink-0">
                    <span className="absolute inline-flex size-full animate-ping rounded-full bg-cyan-400 opacity-20 motion-reduce:animate-none" />
                    <span className="relative inline-flex size-3 rounded-full bg-cyan-500" />
                  </span>
                  <div className="min-w-0 flex-1 rounded-lg border border-border/50 px-3 py-2">
                    <div className="flex items-center justify-between gap-2">
                      <span className="text-xs font-medium">
                        {fleetClassName(entry.class_id, entry.class_label)}
                      </span>
                      <span className="text-[10px] text-muted-foreground">
                        {timeAgo(entry.recorded_at)}
                      </span>
                    </div>
                    <p className="mt-0.5 text-[10px] text-muted-foreground">
                      {t(($) => $.honor_agent.fleet_history_value, {
                        score: entry.fleet_score.toFixed(1),
                        rank: entry.fleet_rank,
                        size: entry.fleet_size,
                      })}
                    </p>
                  </div>
                </div>
              ))
            ) : (
              <p className="text-xs text-muted-foreground">
                {t(($) => $.honor_agent.no_history)}
              </p>
            )}
          </div>
        </Panel>
      </div>
    </div>
  );
}

function AgentHonorAdminDialog({
  agent,
  rulesView,
  audit,
}: {
  agent: Agent;
  rulesView: AgentHonorRulesView;
  audit: Array<{
    id: string;
    action: string;
    details: Record<string, unknown>;
    created_at: string;
  }>;
}) {
  const { t } = useT("agents");
  const [open, setOpen] = useState(false);

  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <DialogTrigger
        render={
          <Button
            type="button"
            size="sm"
            variant="outline"
            className="border-white/15 bg-black/20 text-white hover:bg-white/10 hover:text-white"
          />
        }
      >
        <Settings2 className="size-3.5" />
        {t(($) => $.honor_agent.admin)}
      </DialogTrigger>
      <DialogContent className="max-h-[88vh] max-w-4xl overflow-y-auto">
        <DialogHeader>
          <DialogTitle>{t(($) => $.honor_agent.admin_title)}</DialogTitle>
          <DialogDescription>{t(($) => $.honor_agent.admin_hint)}</DialogDescription>
        </DialogHeader>
        <AgentHonorAdminContent
          key={rulesView.revision}
          agent={agent}
          rulesView={rulesView}
          audit={audit}
        />
      </DialogContent>
    </Dialog>
  );
}

export function AgentHonorAdminContent({
  agent,
  rulesView,
  audit,
}: {
  agent: Pick<Agent, "id">;
  rulesView: AgentHonorRulesView;
  audit: Array<{
    id: string;
    action: string;
    details: Record<string, unknown>;
    created_at: string;
  }>;
}) {
  const { t } = useT("agents");
  const achievementCopy = useAgentAchievementCopy();
  const fleetClassName = useAgentFleetClassName();
  const honorCopy = useAgentHonorCopy();
  const timeAgo = useTimeAgo();
  const workspaceId = useWorkspaceId();
  const queryClient = useQueryClient();
  const [{ rules, grantKind, grantXP, achievementId, reason }, dispatch] = useReducer(
    agentHonorAdminReducer,
    rulesView,
    createAgentHonorAdminState,
  );

  const saveRules = useMutation({
    mutationFn: () => api.updateAgentHonorRules(rules),
    onSuccess: (next) => {
      queryClient.setQueryData(agentHonorKeys.rules(workspaceId), next);
      toast.success(t(($) => $.honor_agent.rules_saved));
    },
    onError: () => showErrorToast(t(($) => $.honor_agent.update_error)),
  });
  const grant = useMutation({
    mutationFn: () =>
      grantKind === "xp"
        ? api.grantAgentHonor(agent.id, { kind: "xp", xp: grantXP, reason })
        : api.grantAgentHonor(agent.id, {
            kind: "achievement",
            achievement_id: achievementId,
            reason,
          }),
    onSuccess: (dashboard) => {
      queryClient.setQueryData(agentHonorKeys.dashboard(workspaceId, agent.id), dashboard);
      void queryClient.invalidateQueries({ queryKey: agentHonorKeys.audit(workspaceId, agent.id) });
      dispatch({ type: "set_reason", value: "" });
      toast.success(t(($) => $.honor_agent.grant_saved));
    },
    onError: () => showErrorToast(t(($) => $.honor_agent.update_error)),
  });

  const weightsTotal = useMemo(
    () => fleetPillars.reduce((sum, key) => sum + (rules.fleet_weights[key] ?? 0), 0),
    [rules.fleet_weights],
  );

  return (
    <div className="grid gap-5">
      <section className="rounded-xl border p-4">
        <div className="mb-4 flex items-center justify-between">
          <div>
            <h3 className="text-sm font-semibold">{t(($) => $.honor_agent.rules)}</h3>
            <p className="text-xs text-muted-foreground">
              {t(($) => $.honor_agent.rules_revision, { revision: rulesView.revision })}
            </p>
          </div>
          <Button
            size="sm"
            disabled={saveRules.isPending || Math.abs(weightsTotal - 1) > 0.0001}
            onClick={() => saveRules.mutate()}
          >
            {t(($) => $.honor_agent.save_rules)}
          </Button>
        </div>
        <div className="grid gap-3 sm:grid-cols-3">
          <NumberField
            label={t(($) => $.honor_agent.completion_xp)}
            value={rules.completion_xp}
            min={1}
            max={100}
            onChange={(completion_xp) =>
              dispatch({
                type: "update_rules",
                update: (current) => ({ ...current, completion_xp }),
              })
            }
          />
          <NumberField
            label={t(($) => $.honor_agent.window_days)}
            value={rules.fleet_window_days}
            min={7}
            max={90}
            onChange={(fleet_window_days) =>
              dispatch({
                type: "update_rules",
                update: (current) => ({ ...current, fleet_window_days }),
              })
            }
          />
          <NumberField
            label={t(($) => $.honor_agent.min_samples)}
            value={rules.fleet_min_sample_tasks}
            min={1}
            max={100}
            onChange={(fleet_min_sample_tasks) =>
              dispatch({
                type: "update_rules",
                update: (current) => ({ ...current, fleet_min_sample_tasks }),
              })
            }
          />
        </div>
        <div className="mt-4">
          <div className="mb-2 flex items-center justify-between">
            <h4 className="text-xs font-medium">{t(($) => $.honor_agent.weights)}</h4>
            <span
              className={cn(
                "text-[10px] tabular-nums",
                Math.abs(weightsTotal - 1) > 0.0001
                  ? "text-destructive"
                  : "text-muted-foreground",
              )}
            >
              {(weightsTotal * 100).toFixed(0)}%
            </span>
          </div>
          <div className="grid gap-2 sm:grid-cols-4">
            {fleetPillars.map((key) => (
              <NumberField
                key={key}
                label={t(($) => $.fleet.pillars[key])}
                value={rules.fleet_weights[key] ?? 0}
                min={0}
                max={1}
                step={0.01}
                onChange={(value) =>
                  dispatch({
                    type: "update_rules",
                    update: (current) => ({
                      ...current,
                      fleet_weights: { ...current.fleet_weights, [key]: value },
                    }),
                  })
                }
              />
            ))}
          </div>
        </div>
        <div className="mt-4">
          <h4 className="mb-2 text-xs font-medium">{t(($) => $.honor_agent.thresholds)}</h4>
          <div className="grid gap-2 sm:grid-cols-3">
            {rules.fleet_classes.map((fleetClass, index) => (
              <NumberField
                key={fleetClass.class_id}
                label={fleetClassName(fleetClass.class_id, fleetClass.label)}
                value={fleetClass.score}
                min={0}
                max={100}
                onChange={(score) =>
                  dispatch({
                    type: "update_rules",
                    update: (current) => ({
                      ...current,
                      fleet_classes: current.fleet_classes.map((item, itemIndex) =>
                        itemIndex === index ? { ...item, score } : item,
                      ),
                    }),
                  })
                }
              />
            ))}
          </div>
        </div>
        <div className="mt-4">
          <h4 className="mb-2 text-xs font-medium">
            {t(($) => $.honor_agent.achievement_rules)}
          </h4>
          <div className="grid gap-2 sm:grid-cols-2">
            {rulesView.achievements.map((achievement) => {
              const achievementTitle = achievementCopy(achievement).title;
              return (
                <div
                  key={achievement.id}
                  className="flex items-center gap-3 rounded-lg border border-border/60 p-2.5"
                >
                  <Switch
                    aria-label={t(($) => $.honor_agent.achievement_enabled_label, {
                      achievement: achievementTitle,
                    })}
                    checked={rules.achievement_enabled[achievement.id] !== false}
                    onCheckedChange={(checked) =>
                      dispatch({
                        type: "update_rules",
                        update: (current) => ({
                          ...current,
                          achievement_enabled: {
                            ...current.achievement_enabled,
                            [achievement.id]: checked,
                          },
                        }),
                      })
                    }
                  />
                  <div className="min-w-0 flex-1">
                    <p className="truncate text-xs font-medium">{achievementTitle}</p>
                    <p className="text-[10px] text-muted-foreground">
                      {honorCopy.metricName(achievement.metric)}
                    </p>
                  </div>
                  <Input
                    aria-label={t(($) => $.honor_agent.achievement_target_label, {
                      achievement: achievementTitle,
                    })}
                    type="number"
                    min={1}
                    className="h-8 w-20 text-xs"
                    value={rules.achievement_targets[achievement.id] ?? achievement.target}
                    onChange={(event) =>
                      dispatch({
                        type: "update_rules",
                        update: (current) => ({
                          ...current,
                          achievement_targets: {
                            ...current.achievement_targets,
                            [achievement.id]: Number(event.target.value),
                          },
                        }),
                      })
                    }
                  />
                </div>
              );
            })}
          </div>
        </div>
      </section>

      <section className="rounded-xl border p-4">
        <h3 className="text-sm font-semibold">{t(($) => $.honor_agent.manual_grant)}</h3>
        <p className="mt-0.5 text-xs text-muted-foreground">
          {t(($) => $.honor_agent.manual_grant_hint)}
        </p>
        <div className="mt-3 grid gap-3 sm:grid-cols-[160px_1fr]">
          <select
            aria-label={t(($) => $.honor_agent.grant_kind_label)}
            value={grantKind}
            onChange={(event) =>
              dispatch({
                type: "set_grant_kind",
                value: event.target.value as "xp" | "achievement",
              })
            }
            className="h-9 rounded-md border border-input bg-background px-3 text-sm"
          >
            <option value="xp">{t(($) => $.honor_agent.xp_label)}</option>
            <option value="achievement">{t(($) => $.honor_agent.achievement)}</option>
          </select>
          {grantKind === "xp" ? (
            <Input
              aria-label={t(($) => $.honor_agent.xp_amount_label)}
              type="number"
              min={-10000}
              max={10000}
              value={grantXP}
              onChange={(event) =>
                dispatch({ type: "set_grant_xp", value: Number(event.target.value) })
              }
            />
          ) : (
            <select
              aria-label={t(($) => $.honor_agent.achievement_select_label)}
              value={achievementId}
              onChange={(event) =>
                dispatch({ type: "set_achievement_id", value: event.target.value })
              }
              className="h-9 rounded-md border border-input bg-background px-3 text-sm"
            >
              {rulesView.achievements.map((achievement) => (
                <option key={achievement.id} value={achievement.id}>
                  {achievementCopy(achievement).title}
                </option>
              ))}
            </select>
          )}
        </div>
        <div className="mt-3 flex gap-2">
          <Input
            aria-label={t(($) => $.honor_agent.reason_label)}
            value={reason}
            onChange={(event) =>
              dispatch({ type: "set_reason", value: event.target.value })
            }
            placeholder={t(($) => $.honor_agent.reason_placeholder)}
          />
          <Button
            disabled={
              grant.isPending ||
              reason.trim() === "" ||
              (grantKind === "xp" && grantXP === 0) ||
              (grantKind === "achievement" && achievementId === "")
            }
            onClick={() => grant.mutate()}
          >
            {t(($) => $.honor_agent.grant)}
          </Button>
        </div>
      </section>

      <section className="rounded-xl border p-4">
        <h3 className="text-sm font-semibold">{t(($) => $.honor_agent.audit)}</h3>
        <div className="mt-3 divide-y divide-border/60">
          {audit.length > 0 ? (
            audit.slice(0, 10).map((entry) => (
              <div key={entry.id} className="flex items-center gap-3 py-2 text-xs">
                <ShieldCheck className="size-4 text-muted-foreground" />
                <span className="flex-1 font-medium">
                  {honorCopy.auditActionName(entry.action)}
                </span>
                <span className="text-muted-foreground">{timeAgo(entry.created_at)}</span>
              </div>
            ))
          ) : (
            <p className="text-xs text-muted-foreground">{t(($) => $.honor_agent.no_audit)}</p>
          )}
        </div>
      </section>
    </div>
  );
}

function NumberField({
  label,
  value,
  min,
  max,
  step = 1,
  onChange,
}: {
  label: string;
  value: number;
  min: number;
  max: number;
  step?: number;
  onChange: (value: number) => void;
}) {
  return (
    <label className="grid gap-1 text-[10px] text-muted-foreground">
      {label}
      <Input
        type="number"
        value={value}
        min={min}
        max={max}
        step={step}
        className="h-8 text-xs text-foreground"
        onChange={(event) => onChange(Number(event.target.value))}
      />
    </label>
  );
}
