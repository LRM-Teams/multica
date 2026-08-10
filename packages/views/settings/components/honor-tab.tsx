"use client";

import { useMemo } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import type { HonorDashboard } from "@multica/core/types/honor";
import { api } from "@multica/core/api";
import { useAuthStore } from "@multica/core/auth";
import { resolveActorDisplayName } from "@multica/core/identity";
import { HonorBadgeCrest } from "@multica/ui/components/honor/honor-badge";
import { Button } from "@multica/ui/components/ui/button";
import { Progress } from "@multica/ui/components/ui/progress";
import { Skeleton } from "@multica/ui/components/ui/skeleton";
import { showErrorToast } from "@multica/ui/lib/error-toast";
import {
  Activity,
  BadgeCheck,
  CircleCheckBig,
  CircleDashed,
  FilePlus2,
  Gem,
  MessageSquareText,
  MessagesSquare,
  Microscope,
  Orbit,
  PencilLine,
  RotateCcw,
  Sparkles,
  Target,
  Timer,
  Trophy,
  UserPlus,
  Zap,
} from "lucide-react";
import { ActorStyledName } from "../../common/actor-styled-name";
import { UserHonorLevelIcon } from "../../honor/user-honor-level-icon";
import { HonorBadgeCatalog } from "../../honor/honor-badge-catalog";
import { HonorNextTargets } from "../../honor/honor-next-targets";
import {
  getHonorShowcaseBadges,
  honorLevelProgress,
  isRareHonorBadge,
} from "../../honor/honor-progress";
import { HONOR_HERO_IMAGE_URL } from "../../honor/honor-assets";
import { useHonorBadgeCopy } from "../../honor/use-honor-badge-copy";
import { useT } from "../../i18n";

const honorKeys = {
  me: ["honor", "me"] as const,
  rules: ["honor", "rules"] as const,
};

const maxShowcaseBadges = 3;
const honorActionOrder = [
  "issue.create",
  "issue.update",
  "issue.close",
  "comment.create",
  "channel.message",
  "research.session",
  "member.invite",
  "presence.minute",
] as const;
export function HonorTab() {
  const { t, i18n } = useT("settings");
  const honorBadgeCopy = useHonorBadgeCopy();
  const qc = useQueryClient();
  const user = useAuthStore((state) => state.user);
  const {
    data: dashboard,
    isLoading,
    isError,
    refetch,
  } = useQuery({
    queryKey: honorKeys.me,
    queryFn: () => api.getMyHonor(),
  });
  const { data: rules } = useQuery({
    queryKey: honorKeys.rules,
    queryFn: () => api.getHonorRules(),
  });

  const catalog = useMemo(
    () =>
      (dashboard?.badge_catalog ?? []).map((badge) => {
        const copy = honorBadgeCopy(badge);
        return {
          ...badge,
          title: copy.title,
          description: copy.description,
          unlock_rule: copy.unlockRule,
          progress: badge.progress
            ? { ...badge.progress, label: copy.progressLabel }
            : undefined,
        };
      }),
    [dashboard?.badge_catalog, honorBadgeCopy],
  );
  const unlockedBadges = useMemo(
    () =>
      (dashboard?.unlocked_badges ?? []).map((badge) => {
        const copy = honorBadgeCopy(badge);
        return { ...badge, title: copy.title, description: copy.description };
      }),
    [dashboard?.unlocked_badges, honorBadgeCopy],
  );
  const recentUnlocks = useMemo(
    () =>
      (dashboard?.recent_unlocks ?? []).map((badge) => {
        const copy = honorBadgeCopy({ ...badge, unlocked: true });
        return { ...badge, title: copy.title, description: copy.description };
      }),
    [dashboard?.recent_unlocks, honorBadgeCopy],
  );

  const locale = i18n.resolvedLanguage || i18n.language;
  const numberFormatter = useMemo(
    () => new Intl.NumberFormat(locale, { maximumFractionDigits: 0 }),
    [locale],
  );
  const rarityFormatter = useMemo(
    () => new Intl.NumberFormat(locale, { maximumFractionDigits: 1 }),
    [locale],
  );
  const dateFormatter = useMemo(
    () =>
      new Intl.DateTimeFormat(locale, {
        month: "short",
        day: "numeric",
      }),
    [locale],
  );

  const equip = useMutation({
    mutationFn: (badgeId: string) =>
      api.updateMyHonor({ equipped_badge_id: badgeId }),
    onMutate: async (badgeId) => {
      await qc.cancelQueries({ queryKey: honorKeys.me });
      const previous = qc.getQueryData<HonorDashboard>(honorKeys.me);
      qc.setQueryData<HonorDashboard>(honorKeys.me, (current) =>
        current
          ? {
              ...current,
              equipped_badge_id: badgeId,
              equipped_badge_manual: true,
            }
          : current,
      );
      return { previous };
    },
    onError: (_error, _badgeId, context) => {
      qc.setQueryData(honorKeys.me, context?.previous);
      showErrorToast(t(($) => $.honor.update_error));
    },
    onSettled: () => qc.invalidateQueries({ queryKey: honorKeys.me }),
  });
  const resetAuto = useMutation({
    mutationFn: () => api.updateMyHonor({ equipped_badge_id: "" }),
    onMutate: async () => {
      await qc.cancelQueries({ queryKey: honorKeys.me });
      const previous = qc.getQueryData<HonorDashboard>(honorKeys.me);
      qc.setQueryData<HonorDashboard>(honorKeys.me, (current) =>
        current
          ? {
              ...current,
              equipped_badge_manual: false,
            }
          : current,
      );
      return { previous };
    },
    onError: (_error, _variables, context) => {
      qc.setQueryData(honorKeys.me, context?.previous);
      showErrorToast(t(($) => $.honor.update_error));
    },
    onSettled: () => qc.invalidateQueries({ queryKey: honorKeys.me }),
  });
  const showcase = useMutation({
    mutationFn: (badgeIds: string[]) =>
      api.updateMyHonor({ showcase_badge_ids: badgeIds }),
    onMutate: async (badgeIds) => {
      await qc.cancelQueries({ queryKey: honorKeys.me });
      const previous = qc.getQueryData<HonorDashboard>(honorKeys.me);
      qc.setQueryData<HonorDashboard>(honorKeys.me, (current) =>
        current ? { ...current, showcase_badge_ids: badgeIds } : current,
      );
      return { previous };
    },
    onError: (_error, _badgeIds, context) => {
      qc.setQueryData(honorKeys.me, context?.previous);
      showErrorToast(t(($) => $.honor.update_error));
    },
    onSettled: () => qc.invalidateQueries({ queryKey: honorKeys.me }),
  });

  if (isLoading) {
    return <HonorTabSkeleton />;
  }

  if (!dashboard) {
    return (
      <div className="rounded-2xl border border-destructive/20 bg-destructive/5 px-6 py-12 text-center">
        <p className="text-sm text-muted-foreground">
          {t(($) => $.honor.load_error)}
        </p>
        <Button
          type="button"
          variant="outline"
          className="mt-4"
          onClick={() => refetch()}
        >
          {t(($) => $.honor.retry)}
        </Button>
      </div>
    );
  }

  const showcaseIds = dashboard.showcase_badge_ids ?? [];
  const showcasedBadges = getHonorShowcaseBadges(
    catalog,
    showcaseIds,
    maxShowcaseBadges,
  );
  const visibleShowcaseIds = showcasedBadges.map((badge) => badge.id);
  const equippedBadge =
    catalog.find((item) => item.id === dashboard.equipped_badge_id) ??
    unlockedBadges.find(
      (item) => item.id === dashboard.equipped_badge_id,
    );
  const nameStyleRules = [...(rules?.name_style_unlocks ?? [])]
    .filter((item) => item.id !== "founding")
    .sort((left, right) => left.min_level - right.min_level);
  const unlocked =
    dashboard.badges_unlocked ?? dashboard.unlocked_badges.length;
  const total = dashboard.badges_total ?? catalog.length;
  const completionPct = total > 0 ? Math.round((unlocked / total) * 100) : 0;
  const levelProgress = honorLevelProgress(
    dashboard.total_xp,
    dashboard.level,
    rules?.level_thresholds ?? [],
    dashboard.xp_to_next_level,
  );
  const displayName = resolveActorDisplayName(
    user,
    user?.name || t(($) => $.honor.anonymous_builder),
  );
  const activity = [
    ...recentUnlocks.map((item) => ({
      kind: "unlock" as const,
      id: `${item.id}-${item.unlocked_at}`,
      title: item.title,
      date: item.unlocked_at,
      svgKey: item.svg_key,
    })),
    ...dashboard.recent_xp.map((event, index) => ({
      kind: "xp" as const,
      id: `${event.created_at}-${event.action_type}-${index}`,
      title: actionLabel(event.action_type, t),
      date: event.created_at,
      xp: event.xp_delta,
    })),
  ]
    .sort((left, right) => right.date.localeCompare(left.date))
    .slice(0, 8);

  const toggleShowcase = (badgeId: string) => {
    const next = visibleShowcaseIds.includes(badgeId)
      ? visibleShowcaseIds.filter((id) => id !== badgeId)
      : [...visibleShowcaseIds, badgeId].slice(-maxShowcaseBadges);
    showcase.mutate(next);
  };

  return (
    <div className="space-y-7 pb-4">
      <section className="honor-dark-surface relative isolate min-h-[340px] overflow-hidden rounded-[1.75rem] border border-cyan-400/20 bg-slate-950 text-white shadow-[0_30px_90px_-50px_rgba(34,211,238,0.7)]">
        <img
          src={HONOR_HERO_IMAGE_URL}
          alt=""
          aria-hidden="true"
          className="absolute inset-0 size-full object-cover object-center opacity-85"
        />
        <div
          aria-hidden="true"
          className="absolute inset-0 bg-[linear-gradient(90deg,rgba(2,6,23,1)_0%,rgba(2,6,23,0.96)_34%,rgba(2,6,23,0.52)_68%,rgba(2,6,23,0.16)_100%)]"
        />
        <div
          aria-hidden="true"
          className="absolute inset-0 bg-[linear-gradient(0deg,rgba(2,6,23,0.9),transparent_54%)]"
        />
        <UserHonorLevelIcon
          level={dashboard.level}
          title={t(($) => $.honor.level_value, { level: dashboard.level })}
          className="absolute right-8 top-1/2 z-10 hidden size-44 -translate-y-1/2 drop-shadow-[0_0_30px_rgba(99,102,241,0.4)] lg:block"
          priority
        />
        <div className="relative flex min-h-[340px] max-w-2xl flex-col justify-between p-6 sm:p-8">
          <div>
            <div className="mb-5 inline-flex items-center gap-2 rounded-full border border-cyan-300/20 bg-cyan-300/10 px-3 py-1 font-mono text-[10px] uppercase tracking-[0.18em] text-cyan-200">
              <Orbit className="size-3.5" aria-hidden="true" />
              {t(($) => $.honor.eyebrow)}
            </div>
            <h2 className="max-w-xl text-balance text-3xl font-semibold tracking-[-0.04em] sm:text-4xl">
              {t(($) => $.honor.title)}
            </h2>
            <p className="mt-3 max-w-lg text-sm leading-6 text-slate-300">
              {t(($) => $.honor.hero_subtitle)}
            </p>
            <div className="mt-5 flex flex-wrap items-center gap-3">
              <div className="rounded-xl border border-white/10 bg-white/5 px-3 py-2 backdrop-blur-sm">
                <ActorStyledName
                  displayName={displayName}
                  honor={{
                    level: dashboard.level,
                    name_style: dashboard.name_style,
                    equipped_badge: equippedBadge,
                  }}
                  honorSurface="profile"
                  nameClassName="text-base font-semibold"
                />
              </div>
              <span className="rounded-full border border-violet-300/20 bg-violet-300/10 px-3 py-1.5 font-mono text-xs text-violet-200">
                {t(($) => $.honor.level_value, { level: dashboard.level })}
              </span>
            </div>
          </div>

          <div className="mt-8 grid gap-5 sm:grid-cols-[minmax(0,1fr)_minmax(220px,0.8fr)] sm:items-end">
            <div>
              <p className="text-xs font-medium uppercase tracking-[0.14em] text-slate-400">
                {t(($) => $.honor.build_score)}
              </p>
              <p className="mt-1 font-mono text-4xl font-semibold tracking-[-0.05em] text-white">
                {numberFormatter.format(dashboard.total_xp)}
                <span className="ml-2 text-sm tracking-normal text-cyan-300">
                  {t(($) => $.honor.xp_label)}
                </span>
              </p>
            </div>
            <div>
              <div className="mb-2 flex justify-between gap-3 text-[11px] text-slate-300">
                <span>{t(($) => $.honor.next_level)}</span>
                <span className="font-mono tabular-nums">
                  {dashboard.xp_to_next_level > 0
                    ? t(($) => $.honor.xp_to_next, {
                        xp: numberFormatter.format(dashboard.xp_to_next_level),
                      })
                    : t(($) => $.honor.max_level)}
                </span>
              </div>
              <Progress
                aria-label={t(($) => $.honor.next_level)}
                value={levelProgress}
                className="[&_[data-slot=progress-indicator]]:bg-gradient-to-r [&_[data-slot=progress-indicator]]:from-cyan-300 [&_[data-slot=progress-indicator]]:to-violet-400 [&_[data-slot=progress-track]]:h-2 [&_[data-slot=progress-track]]:bg-white/10"
              />
            </div>
          </div>
        </div>
      </section>

      {isError ? (
        <div className="rounded-xl border border-amber-500/25 bg-amber-500/5 px-4 py-3 text-xs text-amber-800 dark:text-amber-200">
          {t(($) => $.honor.stale_data_warning)}
        </div>
      ) : null}

      <section aria-labelledby="honor-next-title">
        <SectionHeading
          id="honor-next-title"
          icon={<Target className="size-4" aria-hidden="true" />}
          title={t(($) => $.honor.next_rewards_title)}
          hint={t(($) => $.honor.next_rewards_hint)}
        />
        <HonorNextTargets
          items={catalog}
          emptyLabel={t(($) => $.honor.next_rewards_empty)}
          progressLabel={(current, target) =>
            t(($) => $.honor.progress_value, {
              current: numberFormatter.format(current),
              target: numberFormatter.format(target),
            })
          }
          rarityLabel={(pct) =>
            t(($) => $.honor.rarity_pct, { pct: rarityFormatter.format(pct) })
          }
        />
      </section>

      {nameStyleRules.length > 0 ? (
        <section
          aria-labelledby="honor-nameplate-title"
          className="honor-dark-surface overflow-hidden rounded-2xl border border-violet-500/20 bg-[linear-gradient(135deg,rgba(15,23,42,1),rgba(30,27,75,0.94)_48%,rgba(8,47,73,0.92))] p-4 text-white shadow-[0_20px_60px_-44px_rgba(34,211,238,0.75)] sm:p-5"
        >
          <SectionHeading
            id="honor-nameplate-title"
            icon={<Sparkles className="size-4" aria-hidden="true" />}
            title={t(($) => $.honor.nameplate_progression_title)}
            hint={t(($) => $.honor.nameplate_progression_hint)}
            dark
          />
          <div className="grid gap-2 sm:grid-cols-4 xl:grid-cols-8">
            {nameStyleRules.map((styleRule) => {
              const isCurrent = dashboard.name_style === styleRule.id;
              const isUnlocked =
                dashboard.unlocked_styles.includes(styleRule.id) ||
                dashboard.level >= styleRule.min_level;

              return (
                <article
                  key={styleRule.id}
                  className={`relative min-w-0 overflow-hidden rounded-xl border px-3 py-3 ${
                    isCurrent
                      ? "border-cyan-300/45 bg-cyan-300/10 shadow-[0_0_20px_-10px_rgba(103,232,249,0.8)]"
                      : "border-white/10 bg-white/[0.045]"
                  }`}
                  data-current={isCurrent ? "true" : undefined}
                >
                  <div className="mb-3 flex items-center justify-between gap-2">
                    <span className="font-mono text-[10px] uppercase tracking-[0.12em] text-slate-400">
                      {t(($) => $.honor.level_value, {
                        level: styleRule.min_level,
                      })}
                    </span>
                    <span
                      className={`size-1.5 rounded-full ${
                        isCurrent
                          ? "bg-cyan-300 shadow-[0_0_8px_rgba(103,232,249,0.95)]"
                          : isUnlocked
                            ? "bg-emerald-400"
                            : "bg-slate-600"
                      }`}
                      aria-hidden="true"
                    />
                  </div>
                  <ActorStyledName
                    displayName={displayName}
                    honor={{
                      level: styleRule.min_level,
                      name_style: styleRule.id,
                    }}
                    honorSurface="profile"
                    nameClassName="text-sm font-semibold"
                  />
                  <p className="mt-2 truncate text-[10px] text-slate-400">
                    {isCurrent
                      ? t(($) => $.honor.nameplate_current)
                      : isUnlocked
                        ? t(($) => $.honor.nameplate_unlocked)
                        : t(($) => $.honor.nameplate_locked)}
                    {" · "}
                    {nameStyleLabel(styleRule.id, t)}
                  </p>
                </article>
              );
            })}
          </div>
        </section>
      ) : null}

      {rules ? (
        <section
          aria-labelledby="honor-earn-title"
          className="rounded-2xl border border-border/70 bg-card p-4 shadow-sm sm:p-5"
        >
          <SectionHeading
            id="honor-earn-title"
            icon={<Zap className="size-4" aria-hidden="true" />}
            title={t(($) => $.honor.earn_title)}
            hint={t(($) => $.honor.earn_hint)}
          />
          <div className="grid gap-2 sm:grid-cols-2 lg:grid-cols-4">
            {honorActionOrder.map((action) => {
              const rule = rules.action_rules[action];
              if (!rule) return null;

              return (
                <article
                  key={action}
                  className="group flex items-center gap-3 rounded-xl border border-border/70 bg-muted/20 p-3 transition-colors hover:border-cyan-500/25 hover:bg-cyan-500/5"
                >
                  <span className="grid size-9 shrink-0 place-items-center rounded-lg border border-cyan-500/15 bg-cyan-500/10 text-cyan-700 dark:text-cyan-300">
                    {actionIcon(action)}
                  </span>
                  <div className="min-w-0 flex-1">
                    <p className="truncate text-xs font-medium">
                      {actionLabel(action, t)}
                    </p>
                    <p className="mt-1 flex flex-wrap items-baseline gap-x-2 gap-y-0.5">
                      <span className="font-mono text-sm font-semibold text-cyan-700 dark:text-cyan-300">
                        {t(($) => $.honor.xp_value, {
                          value: `+${numberFormatter.format(rule.xp_delta)}`,
                        })}
                      </span>
                      <span className="text-[10px] text-muted-foreground">
                        {t(($) => $.honor.daily_cap, {
                          xp: numberFormatter.format(rule.daily_cap),
                        })}
                      </span>
                    </p>
                  </div>
                </article>
              );
            })}
          </div>
        </section>
      ) : null}

      <div className="grid items-start gap-6 xl:grid-cols-[minmax(0,1.45fr)_minmax(300px,0.75fr)]">
        <section
          aria-labelledby="honor-collection-title"
          className="rounded-2xl border border-border/70 bg-card p-4 shadow-sm sm:p-5"
        >
          <SectionHeading
            id="honor-collection-title"
            icon={<Trophy className="size-4" aria-hidden="true" />}
            title={t(($) => $.honor.collection_title)}
            hint={t(($) => $.honor.collection_hint)}
            metric={t(($) => $.honor.completion_percent, {
              pct: completionPct,
            })}
          />
          <HonorBadgeCatalog
            items={catalog}
            equippedBadgeId={dashboard.equipped_badge_id}
            showcaseBadgeIds={visibleShowcaseIds}
            completionLabel={t(($) => $.honor.completion_value, {
              unlocked,
              total,
            })}
            equipLabel={t(($) => $.honor.equip_badge)}
            equippedLabel={t(($) => $.honor.equipped_badge)}
            showcaseLabel={t(($) => $.honor.showcase_badge)}
            showcasedLabel={t(($) => $.honor.showcased_badge)}
            secretLabel={t(($) => $.honor.secret_badge)}
            secretDescription={t(($) => $.honor.secret_description)}
            lockedLabel={t(($) => $.honor.locked_badge)}
            rareLabel={t(($) => $.honor.rare_badge)}
            emptyLabel={t(($) => $.honor.empty_filter)}
            filterLabels={{
              all: t(($) => $.honor.filter_all),
              unlocked: t(($) => $.honor.filter_unlocked),
              locked: t(($) => $.honor.filter_locked),
              rare: t(($) => $.honor.filter_rare),
            }}
            rarityLabel={(pct) =>
              t(($) => $.honor.rarity_pct, {
                pct: rarityFormatter.format(pct),
              })
            }
            editable
            equipPending={equip.isPending || resetAuto.isPending}
            showcasePending={showcase.isPending}
            onEquip={(badgeId) => equip.mutate(badgeId)}
            onToggleShowcase={toggleShowcase}
          />
        </section>

        <div className="space-y-6">
          <section
            aria-labelledby="honor-showcase-title"
            className="overflow-hidden rounded-2xl border border-violet-500/20 bg-[linear-gradient(145deg,rgba(15,23,42,1),rgba(30,27,75,0.94))] p-5 text-white shadow-[0_20px_60px_-42px_rgba(139,92,246,0.9)]"
          >
            <SectionHeading
              id="honor-showcase-title"
              icon={<Sparkles className="size-4" aria-hidden="true" />}
              title={t(($) => $.honor.showcase_title)}
              hint={t(($) => $.honor.showcase_hint)}
              dark
            />
            <div className="grid grid-cols-3 gap-3">
              {Array.from({ length: maxShowcaseBadges }).map((_, index) => {
                const badge = showcasedBadges[index];
                return badge ? (
                  <div
                    key={badge.id}
                    className="flex min-h-32 flex-col items-center justify-center rounded-xl border border-white/10 bg-white/5 p-3 text-center"
                  >
                    <HonorBadgeCrest
                      svgKey={badge.svg_key}
                      title={badge.title}
                      rare={isRareHonorBadge(badge)}
                      animated
                      className="size-14"
                    />
                    <p className="mt-2 line-clamp-2 text-[11px] font-medium leading-4 text-slate-200">
                      {badge.title}
                    </p>
                  </div>
                ) : (
                  <div
                    key={`empty-${index}`}
                    className="flex min-h-32 flex-col items-center justify-center rounded-xl border border-dashed border-white/15 bg-white/[0.025] p-3 text-center text-slate-500"
                  >
                    <CircleDashed className="size-6" aria-hidden="true" />
                    <span className="mt-2 text-[10px]">
                      {t(($) => $.honor.empty_showcase)}
                    </span>
                  </div>
                );
              })}
            </div>
            {dashboard.equipped_badge_manual === true ? (
              <Button
                type="button"
                variant="ghost"
                size="sm"
                className="mt-3 text-slate-300 hover:bg-white/10 hover:text-white"
                disabled={resetAuto.isPending || equip.isPending}
                onClick={() => resetAuto.mutate()}
              >
                <RotateCcw className="size-3.5" aria-hidden="true" />
                {t(($) => $.honor.reset_auto_badge)}
              </Button>
            ) : null}
          </section>

          <section
            aria-labelledby="honor-pillars-title"
            className="rounded-2xl border border-border/70 bg-card p-5 shadow-sm"
          >
            <SectionHeading
              id="honor-pillars-title"
              icon={<Zap className="size-4" aria-hidden="true" />}
              title={t(($) => $.honor.pillars_title)}
              hint={t(($) => $.honor.pillars_hint)}
            />
            <div className="space-y-4">
              {dashboard.pillars.map((pillar) => {
                const pillarPct = pillar.next_tier_at
                  ? Math.min(
                      100,
                      Math.round(
                        (pillar.counter_value / pillar.next_tier_at) * 100,
                      ),
                    )
                  : 100;
                return (
                  <div key={pillar.pillar}>
                    <div className="mb-1.5 flex items-center justify-between gap-2">
                      <span className="text-sm font-medium">
                        {pillarLabel(pillar.pillar, t)}
                      </span>
                      <span className="font-mono text-[11px] text-muted-foreground">
                        {pillar.next_tier_at
                          ? t(($) => $.honor.pillar_tier_progress, {
                              tier: pillar.tier,
                              current: numberFormatter.format(
                                pillar.counter_value,
                              ),
                              next: numberFormatter.format(pillar.next_tier_at),
                            })
                          : t(($) => $.honor.pillar_tier_only, {
                              tier: pillar.tier,
                            })}
                      </span>
                    </div>
                    <Progress
                      aria-label={pillarLabel(pillar.pillar, t)}
                      value={pillarPct}
                      className="[&_[data-slot=progress-indicator]]:bg-gradient-to-r [&_[data-slot=progress-indicator]]:from-violet-500 [&_[data-slot=progress-indicator]]:to-cyan-500 [&_[data-slot=progress-track]]:h-1.5"
                    />
                  </div>
                );
              })}
            </div>
          </section>

          <section
            aria-labelledby="honor-activity-title"
            className="rounded-2xl border border-border/70 bg-card p-5 shadow-sm"
          >
            <SectionHeading
              id="honor-activity-title"
              icon={<Activity className="size-4" aria-hidden="true" />}
              title={t(($) => $.honor.activity_title)}
              hint={t(($) => $.honor.activity_hint)}
            />
            {activity.length > 0 ? (
              <ol className="space-y-1">
                {activity.map((item) => (
                  <li
                    key={item.id}
                    className="flex items-center gap-3 rounded-xl px-2 py-2.5 hover:bg-muted/50"
                  >
                    <span className="grid size-8 shrink-0 place-items-center rounded-lg border border-border bg-muted/40">
                      {item.kind === "unlock" ? (
                        <BadgeCheck
                          className="size-4 text-violet-500"
                          aria-hidden="true"
                        />
                      ) : (
                        <Zap
                          className="size-4 text-cyan-600"
                          aria-hidden="true"
                        />
                      )}
                    </span>
                    <div className="min-w-0 flex-1">
                      <p className="truncate text-xs font-medium">{item.title}</p>
                      <time
                        dateTime={item.date}
                        className="text-[10px] text-muted-foreground"
                      >
                        {dateFormatter.format(new Date(item.date))}
                      </time>
                    </div>
                    {item.kind === "xp" ? (
                      <span className="font-mono text-xs font-semibold text-cyan-700 dark:text-cyan-300">
                        {t(($) => $.honor.xp_value, {
                          value: `${item.xp > 0 ? "+" : ""}${numberFormatter.format(item.xp)}`,
                        })}
                      </span>
                    ) : (
                      <Gem
                        className="size-4 shrink-0 text-violet-500"
                        aria-hidden="true"
                      />
                    )}
                  </li>
                ))}
              </ol>
            ) : (
              <p className="py-6 text-center text-xs text-muted-foreground">
                {t(($) => $.honor.activity_empty)}
              </p>
            )}
          </section>
        </div>
      </div>

      {rules ? (
        <p className="text-center font-mono text-[10px] text-muted-foreground">
          {t(($) => $.honor.rules_hint)} · {rules.version}
        </p>
      ) : null}
    </div>
  );
}

function HonorTabSkeleton() {
  return (
    <div className="space-y-7" aria-busy="true">
      <Skeleton className="h-[340px] rounded-[1.75rem]" />
      <div className="grid gap-3 lg:grid-cols-3">
        {Array.from({ length: 3 }).map((_, index) => (
          <Skeleton key={index} className="h-40 rounded-2xl" />
        ))}
      </div>
      <div className="grid gap-6 xl:grid-cols-[minmax(0,1.45fr)_minmax(300px,0.75fr)]">
        <Skeleton className="h-[560px] rounded-2xl" />
        <Skeleton className="h-[420px] rounded-2xl" />
      </div>
    </div>
  );
}

function SectionHeading({
  id,
  icon,
  title,
  hint,
  metric,
  dark = false,
}: {
  id: string;
  icon: React.ReactNode;
  title: string;
  hint: string;
  metric?: string;
  dark?: boolean;
}) {
  return (
    <div className="mb-4 flex items-start justify-between gap-4">
      <div>
        <h3
          id={id}
          className={`flex items-center gap-2 text-sm font-semibold ${
            dark ? "text-white" : ""
          }`}
        >
          {icon}
          {title}
        </h3>
        <p
          className={`mt-1 text-xs leading-5 ${
            dark ? "text-slate-400" : "text-muted-foreground"
          }`}
        >
          {hint}
        </p>
      </div>
      {metric ? (
        <span className="shrink-0 rounded-full border border-border px-2.5 py-1 font-mono text-[10px] text-muted-foreground">
          {metric}
        </span>
      ) : null}
    </div>
  );
}

type TranslationFunction = ReturnType<typeof useT<"settings">>["t"];

function pillarLabel(pillar: string, t: TranslationFunction): string {
  switch (pillar) {
    case "usage":
      return t(($) => $.honor.pillar_usage);
    case "presence":
      return t(($) => $.honor.pillar_presence);
    case "delivery":
      return t(($) => $.honor.pillar_delivery);
    case "community":
      return t(($) => $.honor.pillar_community);
    default:
      return pillar;
  }
}

function actionLabel(action: string, t: TranslationFunction): string {
  switch (action) {
    case "issue.create":
      return t(($) => $.honor.action_issue_create);
    case "issue.update":
      return t(($) => $.honor.action_issue_update);
    case "issue.close":
      return t(($) => $.honor.action_issue_close);
    case "comment.create":
      return t(($) => $.honor.action_comment_create);
    case "channel.message":
      return t(($) => $.honor.action_channel_message);
    case "member.invite":
      return t(($) => $.honor.action_member_invite);
    case "presence.minute":
      return t(($) => $.honor.action_presence_minute);
    case "research.session":
      return t(($) => $.honor.action_research_session);
    default:
      return t(($) => $.honor.action_fallback);
  }
}

function nameStyleLabel(style: string, t: TranslationFunction): string {
  switch (style) {
    case "default":
      return t(($) => $.honor.nameplate_style_default);
    case "ice":
      return t(($) => $.honor.nameplate_style_ice);
    case "member":
      return t(($) => $.honor.nameplate_style_member);
    case "emerald":
      return t(($) => $.honor.nameplate_style_emerald);
    case "sapphire":
      return t(($) => $.honor.nameplate_style_sapphire);
    case "gold":
      return t(($) => $.honor.nameplate_style_gold);
    case "coral":
      return t(($) => $.honor.nameplate_style_coral);
    case "amethyst":
      return t(($) => $.honor.nameplate_style_amethyst);
    case "prismatic":
      return t(($) => $.honor.nameplate_style_prismatic);
    case "aurora":
      return t(($) => $.honor.nameplate_style_aurora);
    case "glow":
      return t(($) => $.honor.nameplate_style_glow);
    case "solar":
      return t(($) => $.honor.nameplate_style_solar);
    case "shimmer":
      return t(($) => $.honor.nameplate_style_shimmer);
    case "nebula":
      return t(($) => $.honor.nameplate_style_nebula);
    case "cyber":
      return t(($) => $.honor.nameplate_style_cyber);
    case "animated_prismatic":
      return t(($) => $.honor.nameplate_style_animated_prismatic);
    case "plasma":
      return t(($) => $.honor.nameplate_style_plasma);
    case "animated_glow":
      return t(($) => $.honor.nameplate_style_animated_glow);
    case "eclipse":
      return t(($) => $.honor.nameplate_style_eclipse);
    case "nova":
      return t(($) => $.honor.nameplate_style_nova);
    case "quantum":
      return t(($) => $.honor.nameplate_style_quantum);
    case "celestial":
      return t(($) => $.honor.nameplate_style_celestial);
    case "mythic":
      return t(($) => $.honor.nameplate_style_mythic);
    case "transcendent":
      return t(($) => $.honor.nameplate_style_transcendent);
    default:
      return style;
  }
}

function actionIcon(action: string): React.ReactNode {
  const className = "size-4";

  switch (action) {
    case "issue.create":
      return <FilePlus2 className={className} aria-hidden="true" />;
    case "issue.update":
      return <PencilLine className={className} aria-hidden="true" />;
    case "issue.close":
      return <CircleCheckBig className={className} aria-hidden="true" />;
    case "comment.create":
      return <MessageSquareText className={className} aria-hidden="true" />;
    case "channel.message":
      return <MessagesSquare className={className} aria-hidden="true" />;
    case "member.invite":
      return <UserPlus className={className} aria-hidden="true" />;
    case "presence.minute":
      return <Timer className={className} aria-hidden="true" />;
    case "research.session":
      return <Microscope className={className} aria-hidden="true" />;
    default:
      return <Zap className={className} aria-hidden="true" />;
  }
}
