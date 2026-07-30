"use client";

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Medal } from "lucide-react";
import { api } from "@multica/core/api";
import { HonorBadgeIcon, honorNameDisplayProps } from "@multica/ui/components/honor/honor-badge";
import { Button } from "@multica/ui/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@multica/ui/components/ui/card";
import { Progress } from "@multica/ui/components/ui/progress";
import { cn } from "@multica/ui/lib/utils";
import { useT } from "../../i18n";

const honorKeys = {
  me: ["honor", "me"] as const,
  rules: ["honor", "rules"] as const,
};

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

  if (isLoading || !dashboard) {
    return <div className="text-sm text-muted-foreground">{t(($) => $.honor.loading)}</div>;
  }

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
              <div className="text-2xl font-bold">Lv.{dashboard.level}</div>
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
          </div>
          <div>
            <div className="mb-1 flex justify-between text-xs text-muted-foreground">
              <span>{t(($) => $.honor.next_level)}</span>
              <span>{dashboard.xp_to_next_level > 0 ? `+${dashboard.xp_to_next_level} XP` : t(($) => $.honor.max_level)}</span>
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
                Tier {pillar.tier}
                {pillar.next_tier_at ? ` · ${pillar.counter_value}/${pillar.next_tier_at}` : ""}
              </div>
            </div>
          ))}
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>{t(($) => $.honor.badges_title)}</CardTitle>
        </CardHeader>
        <CardContent className="flex flex-wrap gap-2">
          {dashboard.unlocked_badges.map((badge) => {
            const equipped = dashboard.equipped_badge_id === badge.id;
            return (
              <Button
                key={badge.id}
                type="button"
                variant={equipped ? "default" : "outline"}
                size="sm"
                className="gap-2"
                disabled={equip.isPending}
                onClick={() => equip.mutate(badge.id)}
              >
                <HonorBadgeIcon svgKey={badge.svg_key} title={badge.title} />
                {badge.title}
              </Button>
            );
          })}
        </CardContent>
      </Card>

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
