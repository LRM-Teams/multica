"use client";

import { useState } from "react";
import { Check, FileText, Minus, Plus, Trash2, X } from "lucide-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import { showErrorToast } from "@multica/ui/lib/error-toast";
import type { Agent, AgentSkillSuggestion } from "@multica/core/types";
import { api } from "@multica/core/api";
import { useWorkspaceId } from "@multica/core/hooks";
import {
  agentSkillSuggestionKeys,
  agentSkillSuggestionOptions,
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
  const { data: workspaceSkills = [] } = useQuery(skillListOptions(wsId));
  const { data: suggestionResult, isLoading: suggestionsLoading } = useQuery(
    agentSkillSuggestionOptions(wsId, agent.id),
  );
  const suggestions = suggestionResult?.suggestions ?? [];
  const [removing, setRemoving] = useState(false);
  const [showAdd, setShowAdd] = useState(false);

  const decideSuggestion = useMutation({
    mutationFn: ({ suggestionId, decision }: { suggestionId: string; decision: "accept" | "dismiss" }) =>
      api.decideAgentSkillSuggestion(agent.id, suggestionId, { decision }),
    onMutate: async ({ suggestionId }) => {
      const queryKey = agentSkillSuggestionKeys.list(wsId, agent.id);
      await qc.cancelQueries({ queryKey });
      const previous = qc.getQueryData<{ suggestions: AgentSkillSuggestion[] }>(queryKey);
      qc.setQueryData<{ suggestions: AgentSkillSuggestion[] }>(queryKey, (current) => {
        if (!current) return current;
        return {
          suggestions: current.suggestions.filter((item) => item.id !== suggestionId),
        };
      });
      return { previous };
    },
    onSuccess: () => {
      toast.success(t(($) => $.tab_body.skills.suggestion_decision_saved_toast));
    },
    onError: (e, _variables, context) => {
      const queryKey = agentSkillSuggestionKeys.list(wsId, agent.id);
      if (context?.previous) {
        qc.setQueryData(queryKey, context.previous);
      }
      showErrorToast(e instanceof Error ? e.message : t(($) => $.tab_body.skills.suggestion_decision_failed_toast));
    },
    onSettled: () => {
      qc.invalidateQueries({ queryKey: agentSkillSuggestionKeys.list(wsId, agent.id) });
      qc.invalidateQueries({ queryKey: workspaceKeys.agents(wsId) });
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
      showErrorToast(e instanceof Error ? e.message : t(($) => $.tab_body.skills.remove_failed_toast));
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
            <h3 className="text-sm font-medium">{t(($) => $.tab_body.skills.suggestion_title)}</h3>
            <p className="mt-0.5 text-xs text-muted-foreground">
              {t(($) => $.tab_body.skills.suggestion_intro)}
            </p>
          </div>
          {suggestions.length > 0 && (
            <Badge variant="outline">{t(($) => $.tab_body.skills.suggestion_count, { count: suggestions.length })}</Badge>
          )}
        </div>

        {suggestionsLoading ? (
          <div className="rounded-md border border-dashed bg-background/60 px-3 py-6 text-center text-xs text-muted-foreground">
            {t(($) => $.tab_body.skills.suggestion_loading)}
          </div>
        ) : suggestions.length === 0 ? (
          <div className="rounded-md border border-dashed bg-background/60 px-3 py-6 text-center text-xs text-muted-foreground">
            {t(($) => $.tab_body.skills.suggestion_empty)}
          </div>
        ) : (
          <ul className="space-y-1.5">
            {suggestions.map((suggestion) => (
              <AgentSkillSuggestionRow
                key={suggestion.id}
                suggestion={suggestion}
                deciding={
                  decideSuggestion.isPending &&
                  decideSuggestion.variables?.suggestionId === suggestion.id
                }
                onDecide={(decision) => decideSuggestion.mutate({ suggestionId: suggestion.id, decision })}
              />
            ))}
          </ul>
        )}
      </div>

      <SkillAddDialog agent={agent} open={showAdd} onOpenChange={setShowAdd} />
    </div>
  );
}

function AgentSkillSuggestionRow({
  suggestion,
  deciding,
  onDecide,
}: {
  suggestion: AgentSkillSuggestion;
  deciding: boolean;
  onDecide: (decision: "accept" | "dismiss") => void;
}) {
  const { t } = useT("agents");
  const isAdd = suggestion.action === "add";

  return (
    <li className="rounded-md border bg-background px-3 py-2">
      <div className="flex items-start gap-2.5">
        {isAdd ? (
          <Plus className="mt-0.5 h-3.5 w-3.5 shrink-0 text-muted-foreground" />
        ) : (
          <Minus className="mt-0.5 h-3.5 w-3.5 shrink-0 text-muted-foreground" />
        )}
        <div className="min-w-0 flex-1">
          <div className="flex min-w-0 flex-wrap items-center gap-2">
            <span className="truncate text-sm font-medium">{suggestion.skill_name}</span>
            <Badge variant="outline">
              {t(($) => $.tab_body.skills.suggestion_action[suggestion.action])}
            </Badge>
          </div>
          {suggestion.skill_description && (
            <p className="mt-1 line-clamp-2 text-xs text-muted-foreground">
              {suggestion.skill_description}
            </p>
          )}
          {suggestion.reason && (
            <p className="mt-1 text-xs text-muted-foreground/90">{suggestion.reason}</p>
          )}
        </div>
        <div className="flex shrink-0 items-center gap-1">
          <Button
            variant="outline"
            size="sm"
            onClick={() => onDecide("accept")}
            disabled={deciding}
          >
            <Check className="h-3 w-3" />
            {t(($) => $.tab_body.skills.suggestion_accept_action)}
          </Button>
          <Button
            variant="ghost"
            size="icon-sm"
            onClick={() => onDecide("dismiss")}
            disabled={deciding}
            title={t(($) => $.tab_body.skills.suggestion_dismiss_action)}
          >
            <X className="h-3.5 w-3.5" />
          </Button>
        </div>
      </div>
    </li>
  );
}
