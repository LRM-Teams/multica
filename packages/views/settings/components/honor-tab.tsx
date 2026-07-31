"use client";

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Medal } from "lucide-react";
import { api } from "@multica/core/api";
import { honorNameDisplayProps } from "@multica/ui/lib/honor-name-display";
import { Button } from "@multica/ui/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@multica/ui/components/ui/card";
import { Progress } from "@multica/ui/components/ui/progress";
import { cn } from "@multica/ui/lib/utils";
import { HonorBadgeCatalog } from "../../honor/honor-badge-catalog";
import { useT } from "../../i18n";

const honorKeys = {
  me: ["honor", "me"] as const,
  rules: ["honor", "rules"] as const,
};

const maxShowcaseBadges = 3;

export function HonorTab() {
  const { t } = useT("settings");
  const qc = useQueryClient();
  const { data: dashboard, isLoading } = useQuery({
    queryKey: honorKeys.me,
    queryFn: () => api.getMyHonor(),
  });
  const { data: rules } = useQuery({
    queryKey: honorKeys.rules,
    queryFn: () => api.getHonorRules(),
  });

  const equip = useMutation({
    mutationFn: (badgeId: string) => api.updateMyHonor({ equipped_badge_id: badgeId }),
    onSuccess: () => qc.invalidateQueries({ queryKey: honorKeys.me }),
  });
  const resetAuto = useMutation({
    mutationFn: () => api.updateMyHonor({ equipped_badge_id: "" }),
    onSuccess: () => qc.invalidateQueries({ queryKey: honorKeys.me }),
  });
  const showcase = useMutation({
    mutationFn: (badgeIds: string[]) => api.updateMyHonor({ showcase_badge_ids: badgeIds }),
    onSuccess: () => qc.invalidateQueries({ queryKey: honorKeys.me }),
  });

  if (isLoading || !dashboard) {
    return <div className="text-sm text-muted-foreground">{t(($) => $.honor.loading)}</div>;
  }

  const catalog = dashboard.badge_catalog ?? [];
  const showcaseIds = dashboard.showcase_badge_ids ?? [];
  const unlocked = dashboard.badges_unlocked ?? dashboard.unlocked_badges.length;
  const total = dashboard.badges_total ?? catalog.length;

  const levelProgress =
    dashboard.xp_to_next_level > 0
      ? Math.max(
          8,
          Math.min(
            100,
            Math.round(
              (100 * dashboard.total_xp) /
                (dashboard.total_xp + dashboard.xp_to_next_level),
            ),
          ),
        )
      : 100;

  const profileNameDisplay = honorNameDisplayProps({
    nameStyle: dashboard.name_style,
    level: dashboard.level,
    surface: "profile",
  });

  const toggleShowcase = (badgeId: string) => {
    const next = showcaseIds.includes(badgeId)
      ? showcaseIds.filter((id) => id !== badgeId)
      : [...showcaseIds, badgeId].slice(0, maxShowcaseBadges);
    showcase.mutate(next);
  };

  return (
    <div className="space-y-6">
      <Card>
        <CardHeader>
          <CardTitle className="flex items-center gap-2">
            <Medal className="size-5" />
            {t(($) => $.honor.title)}
          </CardTitle>
        </CardHeader>
        <CardContent className="space-y-4">
          <div className="flex flex-wrap items-end gap-3">
            <div>
              <div className="text-xs text-muted-foreground">{t(($) => $.honor.level)}</div>
              <div className="text-2xl font-bold">
                {t(($) => $.honor.level_value, { level: dashboard.level })}
              </div>
            </div>
            <div>
              <div className="text-xs text-muted-foreground">{t(($) => $.honor.total_xp)}</div>
              <div className="text-lg font-semibold tabular-nums">{dashboard.total_xp}</div>
            </div>
            <div>
              <div className="text-xs text-muted-foreground">{t(($) => $.honor.name_style)}</div>
              <div
                className={cn("text-lg font-semibold", profileNameDisplay.className)}
                data-honor-glow-tier={profileNameDisplay["data-honor-glow-tier"]}
                style={profileNameDisplay.style}
              >
                {dashboard.name_style}
              </div>
            </div>
            <div>
              <div className="text-xs text-muted-foreground">{t(($) => $.honor.completion_title)}</div>
              <div className="text-lg font-semibold tabular-nums">
                {t(($) => $.honor.completion_value, { unlocked, total })}
              </div>
            </div>
          </div>
          <div>
            <div className="mb-1 flex justify-between text-xs text-muted-foreground">
              <span>{t(($) => $.honor.next_level)}</span>
              <span>
                {dashboard.xp_to_next_level > 0
                  ? t(($) => $.honor.xp_to_next, { xp: dashboard.xp_to_next_level })
                  : t(($) => $.honor.max_level)}
              </span>
            </div>
            <Progress value={levelProgress} />
          </div>
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>{t(($) => $.honor.pillars_title)}</CardTitle>
        </CardHeader>
        <CardContent className="grid gap-3 sm:grid-cols-2">
          {dashboard.pillars.map((pillar) => (
            <div key={pillar.pillar} className="rounded-lg border border-border p-3">
              <div className="text-sm font-medium">{pillar.pillar}</div>
              <div className="text-xs text-muted-foreground">
                {pillar.next_tier_at
                  ? t(($) => $.honor.pillar_tier_progress, {
                      tier: pillar.tier,
                      current: pillar.counter_value,
                      next: pillar.next_tier_at,
                    })
                  : t(($) => $.honor.pillar_tier_only, { tier: pillar.tier })}
              </div>
            </div>
          ))}
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>{t(($) => $.honor.badges_title)}</CardTitle>
        </CardHeader>
        <CardContent className="space-y-3">
          <p className="text-xs text-muted-foreground">{t(($) => $.honor.badges_auto_hint)}</p>
          <HonorBadgeCatalog
            items={catalog}
            equippedBadgeId={dashboard.equipped_badge_id}
            showcaseBadgeIds={showcaseIds}
            completionLabel={t(($) => $.honor.completion_value, { unlocked, total })}
            equipLabel={t(($) => $.honor.equip_badge)}
            showcaseLabel={t(($) => $.honor.showcase_badge)}
            secretLabel={t(($) => $.honor.secret_badge)}
            lockedLabel={t(($) => $.honor.locked_badge)}
            rarityLabel={(pct) => t(($) => $.honor.rarity_pct, { pct: pct.toFixed(1) })}
            editable
            equipPending={equip.isPending || resetAuto.isPending}
            showcasePending={showcase.isPending}
            onEquip={(badgeId) => equip.mutate(badgeId)}
            onToggleShowcase={toggleShowcase}
          />
          {dashboard.equipped_badge_manual === true ? (
            <Button
              type="button"
              variant="ghost"
              size="sm"
              disabled={resetAuto.isPending || equip.isPending}
              onClick={() => resetAuto.mutate()}
            >
              {t(($) => $.honor.reset_auto_badge)}
            </Button>
          ) : null}
        </CardContent>
      </Card>

      {dashboard.recent_unlocks && dashboard.recent_unlocks.length > 0 ? (
        <Card>
          <CardHeader>
            <CardTitle>{t(($) => $.honor.recent_unlocks_title)}</CardTitle>
          </CardHeader>
          <CardContent className="space-y-2 text-sm">
            {dashboard.recent_unlocks.map((item) => (
              <div key={`${item.id}-${item.unlocked_at}`} className="flex justify-between gap-3">
                <span className="font-medium">{item.title}</span>
                <span className="text-muted-foreground">{item.unlocked_at.slice(0, 10)}</span>
              </div>
            ))}
          </CardContent>
        </Card>
      ) : null}

      {dashboard.recent_xp.length > 0 ? (
        <Card>
          <CardHeader>
            <CardTitle>{t(($) => $.honor.recent_xp_title)}</CardTitle>
          </CardHeader>
          <CardContent className="space-y-2 text-sm">
            {dashboard.recent_xp.map((event, index) => (
              <div key={`${event.created_at}-${index}`} className="flex justify-between gap-3">
                <span className="text-muted-foreground">{event.action_type}</span>
                <span className="font-medium tabular-nums">+{event.xp_delta}</span>
              </div>
            ))}
          </CardContent>
        </Card>
      ) : null}

      {rules ? (
        <p className="text-xs text-muted-foreground">
          {t(($) => $.honor.rules_hint)} · {rules.version}
        </p>
      ) : null}
    </div>
  );
}
