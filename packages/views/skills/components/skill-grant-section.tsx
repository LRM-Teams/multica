"use client";

import { useMemo, useState } from "react";
import { ArrowUpRight, Loader2 } from "lucide-react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "@multica/core/api";
import { channelsOptions } from "@multica/core/channels/queries";
import { useWorkspaceId } from "@multica/core/hooks";
import {
  skillDetailOptions,
  skillPromotionsOptions,
  workspaceKeys,
} from "@multica/core/workspace/queries";
import type { Skill, SkillGrantLevel } from "@multica/core/types";
import { showErrorToast } from "@multica/ui/lib/error-toast";
import { Button } from "@multica/ui/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@multica/ui/components/ui/dialog";
import { Label } from "@multica/ui/components/ui/label";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@multica/ui/components/ui/select";
import { toast } from "sonner";
import { useT, useTimeAgo } from "../../i18n";

function grantLevelLabel(
  level: SkillGrantLevel,
  labels: { agent: string; channel: string; workspace: string },
): string {
  return labels[level] ?? labels.agent;
}

/**
 * LRM-954 — Skills detail grant badge, promote actions, and promotion audit.
 * Tolerates older servers that omit `grant_level` / promotions routes.
 */
export function SkillGrantSection({ skill }: { skill: Skill }) {
  const { t } = useT("skills");
  const timeAgo = useTimeAgo();
  const wsId = useWorkspaceId();
  const qc = useQueryClient();
  const levelLabels = {
    agent: t(($) => $.detail.grant.level_agent),
    channel: t(($) => $.detail.grant.level_channel),
    workspace: t(($) => $.detail.grant.level_workspace),
  };

  const grantLevel: SkillGrantLevel = skill.grant_level ?? "agent";
  const caps = skill.capabilities;
  const canToChannel = !!caps?.can_promote_to_channel;
  const canToWorkspace = !!caps?.can_promote_to_workspace;

  const { data: channels = [] } = useQuery(channelsOptions(wsId));
  const groupChannels = useMemo(
    () => channels.filter((c) => c.kind === "group" && !c.archived_at),
    [channels],
  );

  const {
    data: promotionsData,
    isError: promotionsError,
    isLoading: promotionsLoading,
  } = useQuery(skillPromotionsOptions(wsId, skill.id));
  const promotions = promotionsData?.items ?? [];

  const [channelDialogOpen, setChannelDialogOpen] = useState(false);
  const [channelId, setChannelId] = useState<string>("");
  const [promoting, setPromoting] = useState(false);

  const channelNameById = useMemo(() => {
    const map = new Map<string, string>();
    for (const c of channels) map.set(c.id, c.name);
    return map;
  }, [channels]);

  const applyUpdatedSkill = (updated: Skill) => {
    qc.setQueryData(skillDetailOptions(wsId, skill.id).queryKey, updated);
    qc.invalidateQueries({
      queryKey: skillPromotionsOptions(wsId, skill.id).queryKey,
    });
    qc.invalidateQueries({
      queryKey: workspaceKeys.skills(wsId),
      exact: true,
    });
  };

  const promoteToWorkspace = async () => {
    setPromoting(true);
    try {
      const updated = await api.promoteSkill(skill.id, {
        to_level: "workspace",
      });
      applyUpdatedSkill(updated);
      toast.success(t(($) => $.detail.grant.toast_promoted_workspace));
    } catch (err) {
      showErrorToast(
        err instanceof Error
          ? err.message
          : t(($) => $.detail.grant.toast_promote_failed),
      );
    } finally {
      setPromoting(false);
    }
  };

  const promoteToChannel = async () => {
    if (!channelId) return;
    setPromoting(true);
    try {
      const updated = await api.promoteSkill(skill.id, {
        to_level: "channel",
        channel_id: channelId,
      });
      applyUpdatedSkill(updated);
      setChannelDialogOpen(false);
      setChannelId("");
      toast.success(t(($) => $.detail.grant.toast_promoted_channel));
    } catch (err) {
      showErrorToast(
        err instanceof Error
          ? err.message
          : t(($) => $.detail.grant.toast_promote_failed),
      );
    } finally {
      setPromoting(false);
    }
  };

  return (
    <div className="space-y-4">
      <div>
        <h3 className="mb-2 text-xs font-medium uppercase tracking-wider text-muted-foreground">
          {t(($) => $.detail.grant.title)}
        </h3>
        <div className="flex flex-wrap items-center gap-2">
          <span className="inline-flex items-center rounded-md border bg-background px-2 py-0.5 text-xs font-medium">
            {grantLevelLabel(grantLevel, levelLabels)}
          </span>
          {grantLevel === "channel" && skill.channel_id ? (
            <span className="truncate text-xs text-muted-foreground">
              {channelNameById.get(skill.channel_id) ??
                t(($) => $.detail.grant.channel_fallback)}
            </span>
          ) : null}
        </div>
        <p className="mt-1.5 text-xs leading-relaxed text-muted-foreground">
          {t(($) => $.detail.grant.hint)}
        </p>
        {(canToChannel || canToWorkspace) && (
          <div className="mt-2 flex flex-col gap-1.5">
            {canToChannel && (
              <Button
                type="button"
                variant="outline"
                size="xs"
                className="justify-start"
                onClick={() => setChannelDialogOpen(true)}
                disabled={promoting}
              >
                <ArrowUpRight className="h-3 w-3" />
                {t(($) => $.detail.grant.promote_channel)}
              </Button>
            )}
            {canToWorkspace && (
              <Button
                type="button"
                variant="outline"
                size="xs"
                className="justify-start"
                onClick={promoteToWorkspace}
                disabled={promoting}
              >
                {promoting ? (
                  <Loader2 className="h-3 w-3 animate-spin" />
                ) : (
                  <ArrowUpRight className="h-3 w-3" />
                )}
                {t(($) => $.detail.grant.promote_workspace)}
              </Button>
            )}
          </div>
        )}
      </div>

      <div>
        <h3 className="mb-2 text-xs font-medium uppercase tracking-wider text-muted-foreground">
          {t(($) => $.detail.grant.audit_title)}
        </h3>
        {promotionsLoading ? (
          <p className="text-xs text-muted-foreground">
            {t(($) => $.detail.grant.audit_loading)}
          </p>
        ) : promotionsError ? (
          <p className="text-xs text-muted-foreground">
            {t(($) => $.detail.grant.audit_unavailable)}
          </p>
        ) : promotions.length === 0 ? (
          <p className="text-xs text-muted-foreground">
            {t(($) => $.detail.grant.audit_empty)}
          </p>
        ) : (
          <ul className="space-y-2">
            {promotions.map((row) => (
              <li
                key={row.id}
                className="rounded-md border bg-background px-2.5 py-2 text-xs"
              >
                <div className="font-medium text-foreground">
                  {grantLevelLabel(row.from_level, levelLabels)}
                  {" → "}
                  {grantLevelLabel(row.to_level, levelLabels)}
                </div>
                <div className="mt-0.5 text-muted-foreground">
                  {row.actor_display_name?.trim() ||
                    t(($) => $.detail.grant.actor_unknown)}
                  {" · "}
                  {timeAgo(row.created_at)}
                </div>
                {row.channel_id ? (
                  <div className="mt-0.5 truncate text-muted-foreground">
                    {channelNameById.get(row.channel_id) ??
                      t(($) => $.detail.grant.channel_fallback)}
                  </div>
                ) : null}
              </li>
            ))}
          </ul>
        )}
      </div>

      <Dialog
        open={channelDialogOpen}
        onOpenChange={(open) => {
          if (!open) {
            setChannelDialogOpen(false);
            setChannelId("");
          }
        }}
      >
        <DialogContent className="sm:max-w-md">
          <DialogHeader>
            <DialogTitle>
              {t(($) => $.detail.grant.channel_dialog_title)}
            </DialogTitle>
            <DialogDescription>
              {t(($) => $.detail.grant.channel_dialog_body)}
            </DialogDescription>
          </DialogHeader>
          <div className="space-y-2">
            <Label htmlFor="skill-promote-channel">
              {t(($) => $.detail.grant.channel_label)}
            </Label>
            <Select
              value={channelId || undefined}
              onValueChange={(value) => {
                if (value) setChannelId(value);
              }}
            >
              <SelectTrigger id="skill-promote-channel" className="w-full">
                <SelectValue
                  placeholder={t(($) => $.detail.grant.channel_placeholder)}
                >
                  {channelId
                    ? (channelNameById.get(channelId) ?? channelId)
                    : null}
                </SelectValue>
              </SelectTrigger>
              <SelectContent>
                {groupChannels.length === 0 ? (
                  <div className="px-2 py-1.5 text-xs text-muted-foreground">
                    {t(($) => $.detail.grant.channel_empty)}
                  </div>
                ) : (
                  groupChannels.map((c) => (
                    <SelectItem key={c.id} value={c.id}>
                      {c.name}
                    </SelectItem>
                  ))
                )}
              </SelectContent>
            </Select>
          </div>
          <DialogFooter>
            <Button
              type="button"
              variant="ghost"
              onClick={() => setChannelDialogOpen(false)}
              disabled={promoting}
            >
              {t(($) => $.detail.grant.cancel)}
            </Button>
            <Button
              type="button"
              onClick={promoteToChannel}
              disabled={promoting || !channelId}
            >
              {promoting ? (
                <>
                  <Loader2 className="h-3 w-3 animate-spin" />
                  {t(($) => $.detail.grant.promoting)}
                </>
              ) : (
                t(($) => $.detail.grant.confirm_channel)
              )}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  );
}
