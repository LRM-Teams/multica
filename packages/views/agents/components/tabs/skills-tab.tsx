"use client";

import { useState } from "react";
import { Check, FileText, Plus, Trash2, X } from "lucide-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import type { Agent, GeneratedSkillDelivery } from "@multica/core/types";
import { api } from "@multica/core/api";
import { useWorkspaceId } from "@multica/core/hooks";
import {
  generatedSkillDeliveryKeys,
  generatedSkillDeliveryOptions,
} from "@multica/core/agents/queries";
import {
  skillListOptions,
  workspaceKeys,
} from "@multica/core/workspace/queries";
import { Badge } from "@multica/ui/components/ui/badge";
import { Button } from "@multica/ui/components/ui/button";
import { SkillAddDialog } from "../skill-add-dialog";
import { useT } from "../../../i18n";

export function SkillsTab({
  agent,
}: {
  agent: Agent;
}) {
  const { t } = useT("agents");
  const qc = useQueryClient();
  const wsId = useWorkspaceId();
  // Same query the SkillAddDialog uses (TanStack Query dedupes by key, so
  // this isn't an extra request) — used here only to grey out the "Add
  // skill" button when the workspace has zero skills total. When skills
  // exist but are all already attached, we still open the dialog: it
  // filters out attached skills and renders a localised "no more skills
  // to add" empty state, which is more useful than a mysterious
  // greyed-out button.
  const { data: workspaceSkills = [] } = useQuery(skillListOptions(wsId));
  const { data: generatedResult, isLoading: generatedLoading } = useQuery(
    generatedSkillDeliveryOptions(wsId, agent.id),
  );
  const generatedDeliveries = generatedResult?.deliveries ?? [];
  const [removing, setRemoving] = useState(false);
  const [showAdd, setShowAdd] = useState(false);

  const decideGeneratedSkill = useMutation({
    mutationFn: ({ deliveryId, decision }: { deliveryId: string; decision: "accepted" | "ignored" | "rejected" }) =>
      api.decideAgentGeneratedSkillDelivery(agent.id, deliveryId, { decision }),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: generatedSkillDeliveryKeys.list(wsId, agent.id) });
    },
    onError: (e) => {
      toast.error(e instanceof Error ? e.message : t(($) => $.tab_body.skills.generated_decision_failed_toast));
    },
  });

  const handleRemove = async (skillId: string) => {
    setRemoving(true);
    try {
      const newIds = agent.skills
        .filter((s) => s.id !== skillId)
        .map((s) => s.id);
      await api.setAgentSkills(agent.id, { skill_ids: newIds });
      qc.invalidateQueries({ queryKey: workspaceKeys.agents(wsId) });
    } catch (e) {
      toast.error(e instanceof Error ? e.message : t(($) => $.tab_body.skills.remove_failed_toast));
    } finally {
      setRemoving(false);
    }
  };

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between gap-3">
        <p className="text-xs text-muted-foreground">
          {t(($) => $.tab_body.skills.intro)}
        </p>
        <Button
          variant="outline"
          size="sm"
          onClick={() => setShowAdd(true)}
          disabled={workspaceSkills.length === 0}
          className="shrink-0"
        >
          <Plus className="h-3 w-3" />
          {t(($) => $.tab_body.skills.add_action)}
        </Button>
      </div>

      {agent.skills.length === 0 ? (
        <div className="flex flex-col items-center justify-center rounded-lg border border-dashed py-12">
          <FileText className="h-8 w-8 text-muted-foreground/40" />
          <p className="mt-3 text-sm text-muted-foreground">
            {t(($) => $.tab_body.skills.empty_title)}
          </p>
          <p className="mt-1 max-w-xs text-center text-xs text-muted-foreground">
            {t(($) => $.tab_body.skills.empty_hint)}
          </p>
          {workspaceSkills.length > 0 && (
            <Button
              onClick={() => setShowAdd(true)}
              size="sm"
              className="mt-3"
            >
              <Plus className="h-3 w-3" />
              {t(($) => $.tab_body.skills.add_action)}
            </Button>
          )}
        </div>
      ) : (
        <ul className="space-y-1.5">
          {agent.skills.map((skill) => (
            <li
              key={skill.id}
              className="flex items-center gap-2.5 rounded-md border px-3 py-2"
            >
              <FileText className="h-3.5 w-3.5 shrink-0 text-muted-foreground" />
              <div className="min-w-0 flex-1">
                <div className="text-sm font-medium">{skill.name}</div>
                {skill.description && (
                  <div className="truncate text-xs text-muted-foreground">
                    {skill.description}
                  </div>
                )}
              </div>
              <Button
                variant="ghost"
                size="icon-sm"
                onClick={() => handleRemove(skill.id)}
                disabled={removing}
                className="text-muted-foreground hover:text-destructive"
              >
                <Trash2 className="h-3.5 w-3.5" />
              </Button>
            </li>
          ))}
        </ul>
      )}

      <div className="space-y-2 rounded-lg border bg-muted/20 p-3">
        <div className="flex items-center justify-between gap-3">
          <div>
            <h3 className="text-sm font-medium">{t(($) => $.tab_body.skills.generated_title)}</h3>
            <p className="mt-0.5 text-xs text-muted-foreground">
              {t(($) => $.tab_body.skills.generated_intro)}
            </p>
          </div>
          {generatedDeliveries.length > 0 && (
            <Badge variant="outline">{t(($) => $.tab_body.skills.generated_count, { count: generatedDeliveries.length })}</Badge>
          )}
        </div>

        {generatedLoading ? (
          <div className="rounded-md border border-dashed bg-background/60 px-3 py-6 text-center text-xs text-muted-foreground">
            {t(($) => $.tab_body.skills.generated_loading)}
          </div>
        ) : generatedDeliveries.length === 0 ? (
          <div className="rounded-md border border-dashed bg-background/60 px-3 py-6 text-center text-xs text-muted-foreground">
            {t(($) => $.tab_body.skills.generated_empty)}
          </div>
        ) : (
          <ul className="space-y-1.5">
            {generatedDeliveries.map((delivery) => (
              <GeneratedSkillDeliveryRow
                key={delivery.id}
                delivery={delivery}
                deciding={decideGeneratedSkill.isPending}
                onDecide={(decision) => decideGeneratedSkill.mutate({ deliveryId: delivery.id, decision })}
              />
            ))}
          </ul>
        )}
      </div>

      <SkillAddDialog agent={agent} open={showAdd} onOpenChange={setShowAdd} />
    </div>
  );
}

function GeneratedSkillDeliveryRow({
  delivery,
  deciding,
  onDecide,
}: {
  delivery: GeneratedSkillDelivery;
  deciding: boolean;
  onDecide: (decision: "accepted" | "ignored" | "rejected") => void;
}) {
  const { t } = useT("agents");
  const enabled = delivery.status === "accepted" && delivery.delivered_path?.includes("/skills/enabled/");
  const statusLabel = enabled
    ? t(($) => $.tab_body.skills.generated_status_enabled)
    : t(($) => $.tab_body.skills.generated_status[delivery.status]) ?? delivery.status;
  const canDecide = delivery.status === "pending" || delivery.status === "delivered" || delivery.status === "accepted";

  return (
    <li className="rounded-md border bg-background px-3 py-2">
      <div className="flex items-start gap-2.5">
        <FileText className="mt-0.5 h-3.5 w-3.5 shrink-0 text-muted-foreground" />
        <div className="min-w-0 flex-1">
          <div className="flex min-w-0 flex-wrap items-center gap-2">
            <span className="truncate text-sm font-medium">{delivery.title}</span>
            <Badge variant={enabled ? "secondary" : "outline"}>{statusLabel}</Badge>
          </div>
          {delivery.canonical_summary && (
            <p className="mt-1 line-clamp-2 text-xs text-muted-foreground">
              {delivery.canonical_summary}
            </p>
          )}
          {delivery.delivered_path && (
            <p className="mt-1 truncate font-mono text-[11px] text-muted-foreground/80" title={delivery.delivered_path}>
              {delivery.delivered_path}
            </p>
          )}
        </div>
        {canDecide && (
          <div className="flex shrink-0 items-center gap-1">
            <Button
              variant="outline"
              size="sm"
              onClick={() => onDecide("accepted")}
              disabled={deciding || enabled}
            >
              <Check className="h-3 w-3" />
              {enabled ? t(($) => $.tab_body.skills.generated_enabled_action) : t(($) => $.tab_body.skills.generated_accept_action)}
            </Button>
            <Button
              variant="ghost"
              size="icon-sm"
              onClick={() => onDecide("ignored")}
              disabled={deciding || delivery.status === "ignored"}
              title={t(($) => $.tab_body.skills.generated_ignore_action)}
            >
              <X className="h-3.5 w-3.5" />
            </Button>
          </div>
        )}
      </div>
    </li>
  );
}
