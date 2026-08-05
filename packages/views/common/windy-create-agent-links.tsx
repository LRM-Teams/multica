"use client";

import * as React from "react";
import { Bot, CheckCircle2, Loader2, X } from "lucide-react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import { showErrorToast } from "@multica/ui/lib/error-toast";
import { api } from "@multica/core/api";
import { useWorkspaceId } from "@multica/core/hooks";
import { useAuthStore } from "@multica/core/auth";
import { memberListOptions, workspaceKeys } from "@multica/core/workspace/queries";
import { deriveRuntimeHealth, runtimeListOptions } from "@multica/core/runtimes";
import type {
  Agent,
  AgentActionCard,
  AgentAvatarSelection,
  CreateAgentRequest,
} from "@multica/core/types";
import { Button } from "@multica/ui/components/ui/button";
import {
  Dialog,
  DialogClose,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@multica/ui/components/ui/dialog";
import { ComputerPicker } from "../agents/components/computer-picker";
import {
  firstRuntimeMachine,
  firstRuntimeIdOnMachine,
  machineForRuntime,
} from "../agents/components/computer-picker-utils";
import { RuntimePicker } from "../agents/components/runtime-picker";
import { ModelDropdown } from "../agents/components/model-dropdown";
import { ThinkingDropdown } from "../agents/components/thinking-dropdown";
import { AvatarPicker, type AvatarPickerSelection } from "../agents/components/avatar-picker";
import { buildRuntimeMachines } from "../runtimes/components/runtime-machines";
import { cn } from "@multica/ui/lib/utils";
import { useT } from "../i18n";

/**
 * Structured hire Action Card for agent:create (Frank/Parker contract A).
 * Trigger: message.parts `reference` with ref_type=action_card, ref_subtype=agent:create.
 * Loads card via GET /api/agents/action-cards/{id}; Create → Dialog + action_card_id.
 * No multica:// markdown deep-link protocol.
 */
export function AgentCreateActionCard({
  cardId,
  label,
  className,
}: {
  cardId: string;
  /** Optional first-paint title from part.label before GET resolves. */
  label?: string | null;
  className?: string;
}) {
  const { t } = useT("agents");
  const id = cardId.trim();

  const {
    data: card,
    isLoading,
    isError,
    error,
    refetch,
  } = useQuery({
    queryKey: ["agent-action-card", id],
    queryFn: () => api.getAgentActionCard(id),
    enabled: !!id,
    staleTime: 30_000,
  });

  const [dialogOpen, setDialogOpen] = React.useState(false);
  const [localStatus, setLocalStatus] = React.useState<
    "prepared" | "done" | "dismissed" | null
  >(null);
  const [createdName, setCreatedName] = React.useState<string | null>(null);
  const [dismissing, setDismissing] = React.useState(false);

  const status = localStatus ?? card?.status ?? "prepared";
  const cardName =
    card?.payload.name?.trim() ||
    label?.trim() ||
    "New Agent";
  const cardDescription = card?.payload.description?.trim() || "";

  const handleCreateClick = () => {
    if (!card || status !== "prepared") return;
    if (card.action_type !== "agent:create") {
      showErrorToast("Unsupported action card type");
      return;
    }
    setDialogOpen(true);
  };

  const handleDismiss = async () => {
    if (!card || status !== "prepared" || dismissing) return;
    setDismissing(true);
    try {
      await api.dismissAgentActionCard(card.id);
      setLocalStatus("dismissed");
      void refetch();
    } catch (err) {
      showErrorToast(err instanceof Error ? err.message : "Failed to dismiss hire card");
    } finally {
      setDismissing(false);
    }
  };

  if (!id) {
    return null;
  }

  if (isLoading) {
    return (
      <div
        className={cn(
          "not-prose my-2 w-full max-w-md rounded-xl border bg-card p-4 shadow-sm",
          className,
        )}
      >
        <div className="flex items-center gap-2 text-sm text-muted-foreground">
          <Loader2 className="size-4 animate-spin" />
          <span>{t(($) => $.windy.loading_card)}</span>
        </div>
      </div>
    );
  }

  if (isError || !card) {
    return (
      <div
        className={cn(
          "not-prose my-2 w-full max-w-md rounded-xl border border-destructive/30 bg-card p-4 shadow-sm",
          className,
        )}
      >
        <p className="text-sm text-destructive">
          {error instanceof Error ? error.message : t(($) => $.windy.load_card_failed)}
        </p>
      </div>
    );
  }

  const isDone = status === "done";
  const isDismissed = status === "dismissed";
  const isPrepared = status === "prepared";

  return (
    <>
      <div
        className={cn(
          "not-prose my-2 w-full max-w-md overflow-hidden rounded-xl border bg-card text-card-foreground shadow-sm",
          (isDone || isDismissed) && "opacity-80",
          className,
        )}
        data-testid="agent-create-action-card"
        data-status={status}
      >
        <div className="border-b bg-muted/30 px-4 py-3">
          <div className="flex items-start gap-3">
            <div className="flex size-9 shrink-0 items-center justify-center rounded-lg border bg-background text-muted-foreground">
              {isDone ? (
                <CheckCircle2 className="size-4 text-success" />
              ) : (
                <Bot className="size-4" />
              )}
            </div>
            <div className="min-w-0 flex-1">
              <p className="text-xs font-medium text-muted-foreground">
                {t(($) => $.windy.hiring_card_badge)}
              </p>
              <p className="mt-0.5 break-words text-sm font-semibold leading-snug">
                {isDone && createdName ? createdName : cardName}
              </p>
              {cardDescription ? (
                <p className="mt-1 break-words text-xs leading-5 text-muted-foreground">
                  {cardDescription}
                </p>
              ) : null}
              {isDone ? (
                <p className="mt-1.5 text-xs text-success">
                  {t(($) => $.windy.card_created, {
                    name: createdName || cardName,
                  })}
                </p>
              ) : null}
              {isDismissed ? (
                <p className="mt-1.5 text-xs text-muted-foreground">
                  {t(($) => $.windy.card_dismissed)}
                </p>
              ) : null}
            </div>
          </div>
        </div>
        {isPrepared ? (
          <div className="flex flex-wrap items-center justify-end gap-2 px-4 py-3">
            <Button
              type="button"
              size="sm"
              variant="ghost"
              disabled={dismissing}
              onClick={() => void handleDismiss()}
            >
              {dismissing ? <Loader2 className="size-3.5 animate-spin" /> : null}
              {t(($) => $.windy.cancel)}
            </Button>
            <Button type="button" size="sm" onClick={handleCreateClick}>
              {t(($) => $.windy.create_agent)}
            </Button>
          </div>
        ) : null}
      </div>
      {dialogOpen && card && (
        <InlineCreateAgentDialog
          card={card}
          onCreated={(name) => {
            setCreatedName(name);
            setLocalStatus("done");
            setDialogOpen(false);
            void refetch();
          }}
          onClose={() => setDialogOpen(false)}
        />
      )}
    </>
  );
}

function InlineCreateAgentDialog({
  card,
  onCreated,
  onClose,
}: {
  card: AgentActionCard;
  onCreated: (name: string) => void;
  onClose: () => void;
  // Hire card fields are independent (avatar vs runtime/model/thinking); a
  // reducer would add indirection without removing any coupling.
  // react-doctor-disable-next-line react-doctor/prefer-useReducer
}) {
  const { t } = useT("agents");
  const { t: tModals } = useT("modals");
  const wsId = useWorkspaceId();
  const currentUser = useAuthStore((s) => s.user);
  const qc = useQueryClient();
  const { data: runtimes = [], isLoading: runtimesLoading } = useQuery(runtimeListOptions(wsId));
  const { data: members = [] } = useQuery(memberListOptions(wsId));
  const [avatarPreviewUrl, setAvatarPreviewUrl] = React.useState<string | null>(null);
  const avatarSelectionRef = React.useRef<AgentAvatarSelection | null>(null);
  const handleAvatarChange = (selection: AvatarPickerSelection | null) => {
    if (selection) {
      setAvatarPreviewUrl(selection.previewUrl);
      avatarSelectionRef.current = { kind: "uploaded", attachment_id: selection.attachmentId };
      return;
    }
    setAvatarPreviewUrl(null);
    avatarSelectionRef.current = null;
  };
  const userId = currentUser?.id ?? null;
  const machines = React.useMemo(
    () => buildRuntimeMachines(runtimes, { now: Date.now(), currentUserId: userId }),
    [runtimes, userId],
  );
  const [selectedMachineId, setSelectedMachineId] = React.useState("");
  const [selectedRuntimeId, setSelectedRuntimeId] = React.useState("");
  const [model, setModel] = React.useState("");
  const [thinkingLevel, setThinkingLevel] = React.useState("");
  const [creating, setCreating] = React.useState(false);

  const cardName = card.payload.name?.trim() || "New Agent";
  const cardDescription = card.payload.description?.trim() || "";

  const handleOpenChange = (open: boolean) => {
    if (!open) onClose();
  };

  const effectiveMachineId =
    selectedMachineId ||
    firstRuntimeMachine(machines)?.id ||
    machineForRuntime(runtimes[0], machines)?.id ||
    "";
  const selectedMachine =
    machines.find((m) => m.id === effectiveMachineId) ?? null;
  const machineRuntimes = selectedMachine?.runtimes ?? [];
  const handleMachineSelect = (machineId: string) => {
    if (machineId === selectedMachineId) return;
    setSelectedMachineId(machineId);
    const next = machines.find((m) => m.id === machineId) ?? null;
    setSelectedRuntimeId(firstRuntimeIdOnMachine(next));
  };

  const effectiveRuntimeId =
    selectedRuntimeId ||
    firstRuntimeIdOnMachine(selectedMachine);
  const selectedRuntime = runtimes.find((r) => r.id === effectiveRuntimeId) ?? null;
  const selectedRuntimeOnline =
    !!selectedRuntime && deriveRuntimeHealth(selectedRuntime, Date.now()) === "online";
  const hasRuntime = runtimes.length > 0;

  const handleCreate = async () => {
    if (!selectedRuntime || creating) return;
    const trimmedModel = model.trim();
    if (!trimmedModel) {
      showErrorToast(t(($) => $.model_dropdown.select_required));
      return;
    }
    setCreating(true);
    try {
      const payload: CreateAgentRequest = {
        display_name: cardName,
        description: cardDescription,
        avatar_selection: avatarSelectionRef.current ?? undefined,
        runtime_id: selectedRuntime.id,
        model: trimmedModel,
        thinking_level: thinkingLevel || undefined,
        max_concurrent_tasks: 6,
        action_card_id: card.id,
      };
      const created = await api.createAgent(payload);
      qc.setQueryData<Agent[]>(workspaceKeys.agents(wsId), (current = []) => [...current, created]);
      qc.invalidateQueries({ queryKey: workspaceKeys.agents(wsId) });
      toast.success(t(($) => $.windy.created_toast, { name: cardName }));
      onCreated(created.display_name || cardName);
    } catch (err) {
      showErrorToast(err instanceof Error ? err.message : "Failed to create agent");
    } finally {
      setCreating(false);
    }
  };

  return (
    <Dialog open onOpenChange={handleOpenChange}>
      <DialogContent
        showCloseButton={false}
        className="bottom-2 top-auto flex max-h-[calc(100dvh-1rem)] w-[calc(100vw-1rem)] max-w-[min(940px,calc(100vw-1rem))] translate-y-0 flex-col gap-0 overflow-hidden border bg-background p-0 shadow-lg sm:top-1/2 sm:bottom-auto sm:max-h-[90vh] sm:w-full sm:max-w-[min(940px,calc(100vw-2rem))] sm:-translate-y-1/2 sm:rounded-xl"
      >
        <DialogHeader className="relative shrink-0 border-b bg-muted/30 px-4 py-4 pr-14 sm:px-6 sm:py-5">
          <DialogClose
            render={
              <Button
                type="button"
                variant="ghost"
                size="icon-sm"
                className="absolute right-3 top-3 z-10 text-muted-foreground hover:text-foreground"
              />
            }
          >
            <X className="size-4" />
            <span className="sr-only">{tModals(($) => $.common.close)}</span>
          </DialogClose>
          <div className="flex min-w-0 items-start gap-3 sm:gap-4">
            <div className="flex size-10 shrink-0 items-center justify-center rounded-lg border bg-background text-muted-foreground">
              <Bot className="size-5" />
            </div>
            <div className="min-w-0 flex-1">
              <p className="mb-1 text-xs font-medium text-muted-foreground">
                {t(($) => $.windy.hiring_card_badge)}
              </p>
              <DialogTitle className="break-words text-lg font-semibold tracking-tight sm:text-xl">
                {t(($) => $.windy.create_title, { name: cardName })}
              </DialogTitle>
              <DialogDescription className="mt-1.5 max-w-2xl text-sm leading-6 text-muted-foreground">
                {t(($) => $.windy.create_description)}
              </DialogDescription>
            </div>
          </div>
        </DialogHeader>
        <div className="min-h-0 flex-1 space-y-4 overflow-y-auto px-4 py-4 sm:space-y-5 sm:px-6 sm:py-5">
          <div className="rounded-lg border bg-card p-4">
            <div className="mb-3 flex items-start gap-3">
              <AvatarPicker
                value={avatarPreviewUrl}
                onChange={handleAvatarChange}
                size={64}
              />
              <div className="min-w-0 flex-1">
                <p className="break-words text-sm font-semibold">{cardDescription || cardName}</p>
                <p className="mt-0.5 text-xs text-muted-foreground">{t(($) => $.windy.generated_hint)}</p>
                <p className="mt-1 text-xs text-muted-foreground">
                  {t(($) => $.windy.avatar_random_hint)}
                </p>
              </div>
            </div>
          </div>

          {!hasRuntime && !runtimesLoading ? (
            <div className="rounded-lg border border-dashed bg-muted/20 px-4 py-6 text-center text-sm text-muted-foreground">
              {t(($) => $.windy.runtime_required)}
            </div>
          ) : (
            <div className="grid gap-4 sm:grid-cols-2">
              <div className="sm:col-span-2">
                <ComputerPicker
                  runtimes={runtimes}
                  runtimesLoading={runtimesLoading}
                  currentUserId={userId}
                  selectedMachineId={effectiveMachineId}
                  onSelect={handleMachineSelect}
                />
              </div>
              <div className="sm:col-span-2">
                <RuntimePicker
                  runtimes={machineRuntimes}
                  runtimesLoading={runtimesLoading}
                  members={members}
                  currentUserId={userId}
                  selectedRuntimeId={effectiveRuntimeId}
                  onSelect={setSelectedRuntimeId}
                  label={t(($) => $.create_dialog.runtime_label)}
                />
              </div>
              <ModelDropdown
                runtimeId={effectiveRuntimeId || null}
                runtimeOnline={selectedRuntimeOnline}
                value={model}
                onChange={setModel}
                disabled={!effectiveRuntimeId}
                required
                autoSelectFirst
              />
              <ThinkingDropdown
                runtimeId={effectiveRuntimeId || null}
                runtimeOnline={selectedRuntimeOnline}
                model={model}
                value={thinkingLevel}
                onChange={setThinkingLevel}
                disabled={!effectiveRuntimeId}
              />
            </div>
          )}
        </div>
        <DialogFooter className="mx-0 mb-0 shrink-0 border-t bg-muted/25 px-4 py-3 sm:px-6 sm:py-4">
          <Button type="button" variant="outline" onClick={onClose} disabled={creating} className="w-full sm:w-auto">
            {t(($) => $.windy.cancel)}
          </Button>
          <Button
            type="button"
            onClick={handleCreate}
            disabled={
              !selectedRuntime ||
              creating ||
              !hasRuntime ||
              !model.trim()
            }
            className="w-full sm:w-auto"
          >
            {creating ? <Loader2 className="size-4 animate-spin" /> : null}
            {t(($) => $.windy.create_agent)}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
