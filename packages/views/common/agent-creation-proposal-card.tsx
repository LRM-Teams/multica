"use client";

import * as React from "react";
import { Bot, CheckCircle2 } from "lucide-react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import { api } from "@multica/core/api";
import { useWorkspaceId } from "@multica/core/hooks";
import { useAuthStore } from "@multica/core/auth";
import { memberListOptions, workspaceKeys } from "@multica/core/workspace/queries";
import { runtimeListOptions } from "@multica/core/runtimes";
import type { Agent, AgentCreationProposal, CreateAgentRequest } from "@multica/core/types";
import { Button } from "@multica/ui/components/ui/button";
import { cn } from "@multica/ui/lib/utils";
import { CreateAgentDialog } from "../agents/components/create-agent-dialog";
import { useT } from "../i18n";

/**
 * Message-backed agent:create Proposal. Its state is read directly from the
 * canonical Message part; no card query, dismiss endpoint, or second
 * client-side state machine exists.
 */
export function AgentCreationProposalCard({
  proposal,
  className,
}: {
  proposal: AgentCreationProposal;
  className?: string;
}) {
  const { t } = useT("agents");
  const wsId = useWorkspaceId();
  const currentUser = useAuthStore((s) => s.user);
  const queryClient = useQueryClient();
  const { data: runtimes = [], isLoading: runtimesLoading } = useQuery(runtimeListOptions(wsId));
  const { data: members = [] } = useQuery(memberListOptions(wsId));
  const [dialogOpen, setDialogOpen] = React.useState(false);

  const canManageAgents = members.some(
    (member) =>
      member.user_id === currentUser?.id &&
      (member.role === "owner" || member.role === "admin"),
  );
  const isPrepared = proposal.status === "prepared";
  const isExecuted = proposal.status === "executed";
  const proposalName = proposal.name.trim() || "New Agent";

  const create = async (data: CreateAgentRequest): Promise<Agent> => {
    const created = await api.createAgent(data);
    queryClient.setQueryData<Agent[]>(workspaceKeys.agents(wsId), (current = []) => {
      if (current.some((agent) => agent.id === created.id)) return current;
      return [...current, created];
    });
    queryClient.invalidateQueries({ queryKey: workspaceKeys.agents(wsId) });
    toast.success(t(($) => $.windy.created_toast, { name: created.display_name || proposalName }));
    return created;
  };

  return (
    <>
      <div
        className={cn(
          "not-prose my-2 w-full max-w-md overflow-hidden rounded-xl border bg-card text-card-foreground shadow-sm",
          isExecuted && "opacity-80",
          className,
        )}
        data-testid="agent-creation-proposal-card"
        data-status={proposal.status}
      >
        <div className="border-b bg-muted/30 px-4 py-3">
          <div className="flex items-start gap-3">
            <div className="flex size-9 shrink-0 items-center justify-center rounded-lg border bg-background text-muted-foreground">
              {isExecuted ? <CheckCircle2 className="size-4 text-success" /> : <Bot className="size-4" />}
            </div>
            <div className="min-w-0 flex-1">
              <p className="text-xs font-medium text-muted-foreground">
                {t(($) => $.windy.hiring_card_badge)}
              </p>
              <p className="mt-0.5 break-words text-sm font-semibold leading-snug">{proposalName}</p>
              {proposal.description ? (
                <p className="mt-1 break-words text-xs leading-5 text-muted-foreground">
                  {proposal.description}
                </p>
              ) : null}
              {proposal.preferred_computer ? (
                <p className="mt-1 text-xs text-muted-foreground">
                  Preferred computer: {proposal.preferred_computer}
                </p>
              ) : null}
              {isExecuted ? (
                <p className="mt-1.5 text-xs text-success">
                  {t(($) => $.windy.card_created, { name: proposalName })}
                </p>
              ) : null}
              {proposal.status === "prepared" && !canManageAgents ? (
                <p className="mt-1.5 text-xs text-muted-foreground">
                  Only workspace owners and admins can create this agent.
                </p>
              ) : null}
            </div>
          </div>
        </div>
        {isPrepared && canManageAgents ? (
          <div className="flex flex-wrap items-center justify-end gap-2 px-4 py-3">
            <Button type="button" size="sm" onClick={() => setDialogOpen(true)}>
              {t(($) => $.windy.create_agent)}
            </Button>
          </div>
        ) : null}
      </div>
      {dialogOpen ? (
        <CreateAgentDialog
          runtimes={runtimes}
          runtimesLoading={runtimesLoading}
          members={members}
          currentUserId={currentUser?.id ?? null}
          proposal={proposal}
          onClose={() => setDialogOpen(false)}
          onCreate={create}
        />
      ) : null}
    </>
  );
}
