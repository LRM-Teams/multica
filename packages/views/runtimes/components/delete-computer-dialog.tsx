"use client";

import { useMemo, useReducer, useRef } from "react";
import { toast } from "sonner";
import { showErrorToast } from "@multica/ui/lib/error-toast";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import type { Agent } from "@multica/core/types";
import { useDeleteComputer } from "@multica/core/runtimes/mutations";
import { useDeleteSandboxMutation } from "@multica/core/sandboxes/mutations";
import { runtimeKeys } from "@multica/core/runtimes/queries";
import { agentListOptions } from "@multica/core/workspace/queries";
import { useWorkspacePresenceMap } from "@multica/core/agents";
import { useWorkspaceId } from "@multica/core/hooks";
import { useWorkspacePaths } from "@multica/core/paths";
import { resolveActorIdentityPresentation } from "@multica/core/identity";
import {
  AlertDialog,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogTitle,
} from "@multica/ui/components/ui/alert-dialog";
import { Button } from "@multica/ui/components/ui/button";
import { Input } from "@multica/ui/components/ui/input";
import { AgentActivityListItem } from "../../agents/components/agent-activity-list-item";
import { useNavigation } from "../../navigation";
import { useT } from "../../i18n";
import {
  isCloudComputerMachine,
  type RuntimeMachine,
} from "./runtime-machines";
import { knownProviderLabel } from "./provider-logo";
import {
  missingDaemonIdConflict,
  missingSandboxIdConflict,
  parseComputerDeleteConflict,
  type ComputerDeleteConflict,
} from "./delete-computer-conflict";

export interface DeleteComputerDialogProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  machine: RuntimeMachine;
  wsId: string;
  canDelete: boolean;
  onDeleted: () => void;
}

interface DeleteDialogState {
  submitting: boolean;
  conflict: ComputerDeleteConflict | null;
  confirmation: string;
}

type DeleteDialogAction =
  | { type: "reset"; conflict: ComputerDeleteConflict | null }
  | { type: "submitting"; value: boolean }
  | { type: "conflict"; conflict: ComputerDeleteConflict }
  | { type: "confirmation"; value: string };

const initialDeleteDialogState: DeleteDialogState = {
  submitting: false,
  conflict: null,
  confirmation: "",
};

function deleteDialogReducer(
  state: DeleteDialogState,
  action: DeleteDialogAction,
): DeleteDialogState {
  switch (action.type) {
    case "reset":
      return { ...initialDeleteDialogState, conflict: action.conflict };
    case "submitting":
      return { ...state, submitting: action.value };
    case "conflict":
      return { ...state, conflict: action.conflict };
    case "confirmation":
      return { ...state, confirmation: action.value };
  }
}

export function DeleteComputerDialog({
  open,
  onOpenChange,
  machine,
  wsId,
  canDelete,
  onDeleted,
}: DeleteComputerDialogProps) {
  const { t } = useT("runtimes");
  const paths = useWorkspacePaths();
  const { data: workspaceAgents = [] } = useQuery(
    agentListOptions(wsId, { includeArchived: true }),
  );
  const [state, dispatch] = useReducer(
    deleteDialogReducer,
    initialDeleteDialogState,
  );
  const { submitting, conflict, confirmation } = state;

  // Seed closed so mount-with-open=true still resets (missing daemon id).
  const prevOpenRef = useRef(false);
  const isCloud = isCloudComputerMachine(machine);
  if (open !== prevOpenRef.current) {
    prevOpenRef.current = open;
    if (open) {
      const missingDaemon =
        !isCloud && !machine.daemonId
          ? missingDaemonIdConflict(
              t(($) => $.machine.delete_computer.blocked_missing_daemon.description, {
                name: machine.title,
              }),
            )
          : isCloud && !machine.sandboxInstanceId
            ? missingSandboxIdConflict(
                t(($) => $.machine.delete_computer.blocked_missing_sandbox.description, {
                  name: machine.title,
                }),
              )
            : null;
      dispatch({
        type: "reset",
        conflict: missingDaemon,
      });
    }
  }

  const queryClient = useQueryClient();
  const deleteMutation = useDeleteComputer(wsId);
  const deleteSandboxMutation = useDeleteSandboxMutation(wsId);
  const affectedAgentCount = useMemo(() => {
    const runtimeIds = new Set(machine.runtimes.map((runtime) => runtime.id));
    const agentIds = new Set<string>();
    for (const agent of workspaceAgents) {
      if (runtimeIds.has(agent.runtime_id)) agentIds.add(agent.id);
    }
    return agentIds.size;
  }, [machine.runtimes, workspaceAgents]);

  const handleConfirm = async () => {
    if (!canDelete) {
      showErrorToast(t(($) => $.list.delete_permission_hint));
      return;
    }

    if (isCloud) {
      const sandboxId = machine.sandboxInstanceId?.trim();
      if (!sandboxId) {
        dispatch({
          type: "conflict",
          conflict: missingSandboxIdConflict(
            t(($) => $.machine.delete_computer.blocked_missing_sandbox.description, {
              name: machine.title,
            }),
          ),
        });
        return;
      }

      dispatch({ type: "submitting", value: true });
      try {
        // Registered cloud computers: clear runtimes first so active-agent
        // conflicts surface before we tear down the container.
        if (machine.daemonId) {
          const result = await deleteMutation.mutateAsync({
            daemonId: machine.daemonId,
          });
          if (result.status !== "ok" || result.daemon_id !== machine.daemonId) {
            throw new Error(t(($) => $.machine.delete_computer.operation_failed.title));
          }
        }

        await deleteSandboxMutation.mutateAsync(sandboxId);
        void queryClient.invalidateQueries({ queryKey: runtimeKeys.all(wsId) });
        toast.success(
          t(($) => $.machine.delete_computer.toast_deleted_cloud, {
            name: machine.title,
          }),
        );
        onDeleted();
      } catch (err) {
        const parsed = parseComputerDeleteConflict(err);
        if (parsed) {
          dispatch({ type: "conflict", conflict: parsed });
          return;
        }
        const message =
          err instanceof Error && err.message
            ? err.message
            : t(($) => $.machine.delete_computer.operation_failed.title);
        showErrorToast(message, {
          description: t(($) => $.machine.delete_computer.operation_failed.description, {
            name: machine.title,
          }),
        });
      } finally {
        dispatch({ type: "submitting", value: false });
      }
      return;
    }

    if (!machine.daemonId) {
      dispatch({
        type: "conflict",
        conflict: missingDaemonIdConflict(
          t(($) => $.machine.delete_computer.blocked_missing_daemon.description, {
            name: machine.title,
          }),
        ),
      });
      return;
    }

    dispatch({ type: "submitting", value: true });
    try {
      const result = await deleteMutation.mutateAsync({
        daemonId: machine.daemonId,
      });
      if (result.status !== "ok" || result.daemon_id !== machine.daemonId) {
        throw new Error(t(($) => $.machine.delete_computer.operation_failed.title));
      }
      toast.success(
        t(($) => $.machine.delete_computer.toast_deleted, {
          name: machine.title,
        }),
      );
      onDeleted();
    } catch (err) {
      const parsed = parseComputerDeleteConflict(err);
      if (parsed) {
        dispatch({ type: "conflict", conflict: parsed });
        return;
      }
      const message =
        err instanceof Error && err.message
          ? err.message
          : t(($) => $.machine.delete_computer.operation_failed.title);
      showErrorToast(message, {
        description: t(($) => $.machine.delete_computer.operation_failed.description, {
          name: machine.title,
        }),
      });
    } finally {
      dispatch({ type: "submitting", value: false });
    }
  };

  const handleOpenChange = (next: boolean) => {
    if (submitting) return;
    onOpenChange(next);
  };

  return (
    <AlertDialog open={open} onOpenChange={handleOpenChange}>
      <AlertDialogContent
        className={
          conflict?.activeAgents.length
            ? "w-[calc(100vw-2rem)] !max-w-[560px] gap-0 overflow-hidden rounded-lg p-0"
            : "w-[calc(100vw-2rem)] !max-w-[440px] gap-0 overflow-hidden rounded-lg p-0"
        }
        onClick={(e) => e.stopPropagation()}
        data-testid="delete-computer-dialog"
      >
        {conflict ? (
          <BlockedBody
            machine={machine}
            conflict={conflict}
            agentHref={(id) => paths.agentDetail(id)}
            onClose={() => handleOpenChange(false)}
          />
        ) : (
          <>
            <div className="px-5 pb-4 pt-5">
              <AlertDialogTitle className="text-base font-semibold">
                {t(($) => $.machine.delete_computer.confirm.title)}
              </AlertDialogTitle>
              <AlertDialogDescription className="mt-1 text-sm leading-5 text-muted-foreground">
                {isCloud
                  ? t(($) => $.machine.delete_computer.confirm.description_cloud, {
                      name: machine.title,
                      count: affectedAgentCount,
                    })
                  : t(($) => $.machine.delete_computer.confirm.description, {
                      name: machine.title,
                      count: affectedAgentCount,
                    })}
              </AlertDialogDescription>
              <label className="mt-4 block text-xs font-medium text-foreground">
                {t(($) => $.machine.delete_computer.confirm.type_to_confirm, {
                  name: machine.title,
                })}
                <Input
                  className="mt-2"
                  value={confirmation}
                  onChange={(event) =>
                    dispatch({
                      type: "confirmation",
                      value: event.target.value,
                    })
                  }
                  disabled={submitting}
                  data-testid="delete-computer-confirmation"
                />
              </label>
            </div>
            <div className="border-t bg-muted/25 px-5 py-3">
              <div className="flex flex-col-reverse gap-2 sm:flex-row sm:justify-end">
                <Button
                  type="button"
                  variant="outline"
                  className="w-full sm:w-auto"
                  onClick={() => handleOpenChange(false)}
                  disabled={submitting}
                >
                  {t(($) => $.machine.delete_computer.confirm.cancel)}
                </Button>
                <Button
                  type="button"
                  variant="destructive"
                  className="w-full sm:w-auto"
                  onClick={() => void handleConfirm()}
                  disabled={submitting || confirmation.trim() !== machine.title}
                  data-testid="delete-computer-confirm"
                >
                  {submitting
                    ? t(($) => $.machine.delete_computer.confirm.submitting)
                    : t(($) => $.machine.delete_computer.confirm.confirm)}
                </Button>
              </div>
            </div>
          </>
        )}
      </AlertDialogContent>
    </AlertDialog>
  );
}

function BlockedBody({
  machine,
  conflict,
  agentHref,
  onClose,
}: {
  machine: RuntimeMachine;
  conflict: ComputerDeleteConflict;
  agentHref: (agentId: string) => string;
  onClose: () => void;
}) {
  const { t } = useT("runtimes");
  const machineTitle = machine.title;

  let title: string;
  let description: string;
  switch (conflict.code) {
    case "computer_has_active_agents":
      title = t(($) => $.machine.delete_computer.blocked_by_agents.title);
      description = t(
        ($) => $.machine.delete_computer.blocked_by_agents.description,
        { name: machineTitle },
      );
      break;
    case "missing_daemon_id":
      title = t(($) => $.machine.delete_computer.blocked_missing_daemon.title);
      description = t(
        ($) => $.machine.delete_computer.blocked_missing_daemon.description,
        { name: machineTitle },
      );
      break;
    case "missing_sandbox_id":
      title = t(($) => $.machine.delete_computer.blocked_missing_sandbox.title);
      description = t(
        ($) => $.machine.delete_computer.blocked_missing_sandbox.description,
        { name: machineTitle },
      );
      break;
  }

  return (
    <>
      <div className="px-5 pb-4 pt-5">
        <AlertDialogTitle className="text-base font-semibold">
          {title}
        </AlertDialogTitle>
        <AlertDialogDescription className="mt-1 text-sm leading-5 text-muted-foreground">
          {description}
        </AlertDialogDescription>

        {conflict.activeAgents.length > 0 && (
          <AgentList
            agents={conflict.activeAgents}
            machine={machine}
            agentHref={agentHref}
          />
        )}
      </div>
      <div className="border-t bg-muted/25 px-5 py-3">
        <div className="flex flex-col-reverse gap-2 sm:flex-row sm:justify-end">
          <Button type="button" variant="outline" onClick={onClose}>
            {t(($) => $.machine.delete_computer.confirm.cancel)}
          </Button>
        </div>
      </div>
    </>
  );
}

function AgentList({
  agents,
  machine,
  agentHref,
}: {
  agents: Agent[];
  machine: RuntimeMachine;
  agentHref: (agentId: string) => string;
}) {
  const { t } = useT("runtimes");
  const wsId = useWorkspaceId();
  const navigation = useNavigation();
  const { byAgent: presenceMap } = useWorkspacePresenceMap(wsId);

  return (
    <div className="mt-3 overflow-hidden rounded-md border divide-y">
      {agents.map((agent) => {
        const presentation = resolveActorIdentityPresentation(agent, agent.id);
        const runtime =
          machine.runtimes.find((r) => r.id === agent.runtime_id) ??
          machine.runtimes[0] ??
          null;
        const runtimeLabel = runtime
          ? (knownProviderLabel(runtime.provider) ?? runtime.provider)
          : undefined;
        return (
          <AgentActivityListItem
            key={agent.id}
            agentId={agent.id}
            displayName={presentation.displayName}
            provider={runtime?.provider}
            runtimeLabel={runtimeLabel}
            presence={presenceMap.get(agent.id)}
            avatarSize={20}
            className="px-3 py-2.5 text-xs hover:bg-muted/40"
            onClick={() => navigation.push(agentHref(agent.id))}
            trailingLabel={t(
              ($) => $.machine.delete_computer.blocked_by_agents.view_agent,
            )}
          />
        );
      })}
    </div>
  );
}
