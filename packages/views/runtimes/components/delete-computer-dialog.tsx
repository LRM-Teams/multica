"use client";

import { useMemo, useRef, useState } from "react";
import { Trash2 } from "lucide-react";
import { toast } from "sonner";
import { useQuery } from "@tanstack/react-query";
import { ApiError } from "@multica/core/api";
import type { Agent } from "@multica/core/types";
import { useAuthStore } from "@multica/core/auth";
import { useDeleteRuntimesByDaemon } from "@multica/core/runtimes/mutations";
import { memberListOptions } from "@multica/core/workspace/queries";
import { useWorkspacePaths } from "@multica/core/paths";
import { resolveActorIdentityPresentation } from "@multica/core/identity";
import {
  AlertDialog,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogTitle,
} from "@multica/ui/components/ui/alert-dialog";
import { Button } from "@multica/ui/components/ui/button";
import { ActorAvatar } from "../../common/actor-avatar";
import { ActorIdentityRow } from "../../common/actor-identity-row";
import { AppLink } from "../../navigation/app-link";
import { useT } from "../../i18n";
import type { RuntimeMachine } from "./runtime-machines";

export type ComputerDeleteConflictCode =
  | "computer_has_online_runtimes"
  | "computer_has_active_agents"
  | "computer_has_active_squads"
  | "computer_has_active_tasks";

interface ComputerDeleteConflict {
  code: ComputerDeleteConflictCode;
  activeAgents: Agent[];
  message: string;
}

/**
 * Machine-header control for Computer one-click delete (LRM-439).
 * Calls `DELETE /api/runtimes/by-daemon/{daemonId}` — never N× row DELETE.
 */
export function MachineDeleteControl({
  machine,
  wsId,
  onDeleted,
}: {
  machine: RuntimeMachine;
  wsId: string;
  onDeleted?: () => void;
}) {
  const { t } = useT("runtimes");
  const user = useAuthStore((s) => s.user);
  const { data: members = [] } = useQuery(memberListOptions(wsId));
  const [open, setOpen] = useState(false);

  const canDelete = useMemo(() => {
    if (!machine.daemonId || machine.runtimes.length === 0) return false;
    const currentMember = user
      ? members.find((m) => m.user_id === user.id)
      : null;
    const isAdmin = currentMember
      ? currentMember.role === "owner" || currentMember.role === "admin"
      : false;
    if (isAdmin) return true;
    if (!user) return false;
    return machine.runtimes.every((r) => r.owner_id === user.id);
  }, [machine.daemonId, machine.runtimes, members, user]);

  if (!canDelete || !machine.daemonId) return null;

  return (
    <>
      <Button
        type="button"
        variant="outline"
        size="sm"
        className="gap-1.5 text-destructive hover:bg-destructive/10 hover:text-destructive"
        onClick={() => setOpen(true)}
      >
        <Trash2 className="h-3.5 w-3.5" aria-hidden />
        {t(($) => $.machine.delete_computer.button)}
      </Button>
      <DeleteComputerDialog
        open={open}
        onOpenChange={setOpen}
        machine={machine}
        daemonId={machine.daemonId}
        wsId={wsId}
        onDeleted={() => {
          setOpen(false);
          onDeleted?.();
        }}
      />
    </>
  );
}

export interface DeleteComputerDialogProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  machine: RuntimeMachine;
  daemonId: string;
  wsId: string;
  onDeleted: () => void;
}

export function DeleteComputerDialog({
  open,
  onOpenChange,
  machine,
  daemonId,
  wsId,
  onDeleted,
}: DeleteComputerDialogProps) {
  const { t } = useT("runtimes");
  const paths = useWorkspacePaths();
  const [submitting, setSubmitting] = useState(false);
  const [conflict, setConflict] = useState<ComputerDeleteConflict | null>(null);

  const prevOpenRef = useRef(open);
  if (open !== prevOpenRef.current) {
    prevOpenRef.current = open;
    if (open) {
      setSubmitting(false);
      setConflict(null);
    }
  }

  const deleteMutation = useDeleteRuntimesByDaemon(wsId);

  const handleConfirm = async () => {
    setSubmitting(true);
    try {
      const result = await deleteMutation.mutateAsync({
        daemonId,
        runtimeMode: machine.mode,
      });
      toast.success(
        t(($) => $.machine.delete_computer.toast_deleted, {
          count: result.deleted_count,
        }),
      );
      onDeleted();
    } catch (err) {
      const parsed = parseComputerDeleteConflict(err);
      if (parsed) {
        setConflict(parsed);
        return;
      }
      const message =
        err instanceof Error && err.message
          ? err.message
          : t(($) => $.machine.delete_computer.delete_failed_toast);
      toast.error(message);
    } finally {
      setSubmitting(false);
    }
  };

  const handleOpenChange = (next: boolean) => {
    if (submitting) return;
    onOpenChange(next);
  };

  const runtimeCount = machine.runtimes.length;

  return (
    <AlertDialog open={open} onOpenChange={handleOpenChange}>
      <AlertDialogContent
        className={
          conflict?.code === "computer_has_active_agents"
            ? "w-[calc(100vw-2rem)] !max-w-[560px] gap-0 overflow-hidden rounded-lg p-0"
            : "w-[calc(100vw-2rem)] !max-w-[440px] gap-0 overflow-hidden rounded-lg p-0"
        }
        onClick={(e) => e.stopPropagation()}
      >
        {conflict ? (
          <BlockedBody
            machineTitle={machine.title}
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
                {t(($) => $.machine.delete_computer.confirm.description, {
                  name: machine.title,
                  count: runtimeCount,
                })}
              </AlertDialogDescription>
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
                  disabled={submitting}
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
  machineTitle,
  conflict,
  agentHref,
  onClose,
}: {
  machineTitle: string;
  conflict: ComputerDeleteConflict;
  agentHref: (agentId: string) => string;
  onClose: () => void;
}) {
  const { t } = useT("runtimes");

  let title: string;
  let description: string;
  switch (conflict.code) {
    case "computer_has_online_runtimes":
      title = t(($) => $.machine.delete_computer.blocked_online.title);
      description = t(
        ($) => $.machine.delete_computer.blocked_online.description,
        { name: machineTitle },
      );
      break;
    case "computer_has_active_agents":
      title = t(($) => $.machine.delete_computer.blocked_by_agents.title, {
        count: conflict.activeAgents.length || 1,
      });
      description = t(
        ($) => $.machine.delete_computer.blocked_by_agents.description,
        { name: machineTitle },
      );
      break;
    case "computer_has_active_squads":
      title = t(($) => $.machine.delete_computer.blocked_squads.title);
      description = t(
        ($) => $.machine.delete_computer.blocked_squads.description,
        { name: machineTitle },
      );
      break;
    case "computer_has_active_tasks":
      title = t(($) => $.machine.delete_computer.blocked_tasks.title);
      description = t(
        ($) => $.machine.delete_computer.blocked_tasks.description,
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

        {conflict.code === "computer_has_active_agents" &&
          conflict.activeAgents.length > 0 && (
            <div className="mt-3 overflow-hidden rounded-md border divide-y">
              {conflict.activeAgents.map((agent) => {
                const presentation = resolveActorIdentityPresentation(
                  agent,
                  agent.id,
                );
                return (
                  <AppLink
                    key={agent.id}
                    href={agentHref(agent.id)}
                    className="flex items-center justify-between gap-3 px-3 py-2.5 text-xs hover:bg-muted/40"
                  >
                    <span className="inline-flex min-w-0 items-center gap-2">
                      <ActorAvatar
                        actorType="agent"
                        actorId={agent.id}
                        size={20}
                        enableHoverCard
                      />
                      <ActorIdentityRow
                        displayName={presentation.displayName}
                        handle={presentation.handle}
                        showHandle={presentation.showHandleLabel}
                        primaryClassName="truncate font-medium text-foreground"
                        secondaryClassName="truncate text-[11px] text-muted-foreground"
                      />
                    </span>
                    <span className="shrink-0 text-primary">
                      {t(
                        ($) =>
                          $.machine.delete_computer.blocked_by_agents
                            .view_agent,
                      )}
                    </span>
                  </AppLink>
                );
              })}
            </div>
          )}
      </div>
      <div className="border-t bg-muted/25 px-5 py-3">
        <div className="flex justify-end">
          <Button type="button" variant="outline" onClick={onClose}>
            {t(($) => $.machine.delete_computer.close)}
          </Button>
        </div>
      </div>
    </>
  );
}

const CONFLICT_CODES = new Set<string>([
  "computer_has_online_runtimes",
  "computer_has_active_agents",
  "computer_has_active_squads",
  "computer_has_active_tasks",
]);

export function parseComputerDeleteConflict(
  err: unknown,
): ComputerDeleteConflict | null {
  if (!(err instanceof ApiError)) return null;
  if (err.status !== 409) return null;
  const body = err.body;
  if (!body || typeof body !== "object") return null;
  const record = body as Record<string, unknown>;
  const code = record.code;
  if (typeof code !== "string" || !CONFLICT_CODES.has(code)) return null;

  const message =
    typeof record.error === "string" && record.error
      ? record.error
      : err.message;

  let activeAgents: Agent[] = [];
  if (code === "computer_has_active_agents") {
    const rawAgents = record.active_agents;
    if (Array.isArray(rawAgents)) {
      activeAgents = rawAgents.filter(
        (a): a is Agent =>
          typeof a === "object" &&
          a !== null &&
          typeof (a as Record<string, unknown>).id === "string" &&
          typeof (a as Record<string, unknown>).name === "string",
      );
    }
  }

  return {
    code: code as ComputerDeleteConflictCode,
    activeAgents,
    message,
  };
}
