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
  firstUsableMachine,
  firstUsableRuntimeIdOnMachine,
  machineForRuntime,
} from "../agents/components/computer-picker-utils";
import { RuntimePicker, isRuntimeUsableForUser } from "../agents/components/runtime-picker";
import { ModelDropdown } from "../agents/components/model-dropdown";
import { ThinkingDropdown } from "../agents/components/thinking-dropdown";
import { AvatarPicker, type AvatarPickerSelection } from "../agents/components/avatar-picker";
import { buildRuntimeMachines } from "../runtimes/components/runtime-machines";
import { cn } from "@multica/ui/lib/utils";
import { useT } from "../i18n";
import { parseAgentCreateActionURL } from "./windy-create-agent-link-utils";

/**
 * Chat-bubble hire CTA for agent:create action cards.
 * Link shape: multica://action-card/agent:create?id=<card-id>
 * Loads card via GET, opens Create dialog prefilled with name/desc,
 * submits POST /api/agents with action_card_id (server marks card done).
 * No draft_id / multica://create-agent?draft_id bridge.
 */
export function WindyCreateAgentLink({
  href,
  children,
  className,
}: {
  href: string;
  children: React.ReactNode;
  className?: string;
}) {
  const [loading, setLoading] = React.useState(false);
  const [card, setCard] = React.useState<AgentActionCard | null>(null);
  const [createdAgentName, setCreatedAgentName] = React.useState<string | null>(null);

  const handleClick = async () => {
    const parsed = parseAgentCreateActionURL(href);
    if (loading || createdAgentName) return;
    if (!parsed) {
      showErrorToast("Invalid Create Agent card link");
      return;
    }
    setLoading(true);
    try {
      const loaded = await api.getAgentActionCard(parsed.cardId);
      if (loaded.status === "done") {
        setCreatedAgentName(loaded.payload.name || "Agent");
        return;
      }
      if (loaded.status === "dismissed") {
        showErrorToast("This hire card was dismissed");
        return;
      }
      if (loaded.action_type !== "agent:create") {
        showErrorToast("Unsupported action card type");
        return;
      }
      setCard(loaded);
    } catch (err) {
      showErrorToast(err instanceof Error ? err.message : "Failed to load hire card");
    } finally {
      setLoading(false);
    }
  };

  return (
    <>
      <Button
        type="button"
        size="sm"
        variant="outline"
        disabled={loading || !!createdAgentName}
        onClick={handleClick}
        className={cn(
          "not-prose my-1 inline-flex max-w-full gap-2 transition-all",
          createdAgentName && "border-muted bg-muted/60 text-muted-foreground shadow-none opacity-80",
          className,
        )}
      >
        {loading ? (
          <Loader2 className="size-3.5 animate-spin" />
        ) : createdAgentName ? (
          <CheckCircle2 className="size-3.5 text-success" />
        ) : (
          <Bot className="size-3.5" />
        )}
        <span className="truncate">{createdAgentName ? `Created: ${createdAgentName}` : children}</span>
      </Button>
      {card && (
        <InlineCreateAgentDialog
          card={card}
          onCreated={(name) => setCreatedAgentName(name)}
          onClose={() => setCard(null)}
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
    firstUsableMachine(machines, userId)?.id ||
    machineForRuntime(
      runtimes.find((r) => isRuntimeUsableForUser(r, userId)),
      machines,
    )?.id ||
    "";
  const selectedMachine =
    machines.find((m) => m.id === effectiveMachineId) ?? null;
  const machineRuntimes = selectedMachine?.runtimes ?? [];
  const handleMachineSelect = (machineId: string) => {
    if (machineId === selectedMachineId) return;
    setSelectedMachineId(machineId);
    const next = machines.find((m) => m.id === machineId) ?? null;
    setSelectedRuntimeId(firstUsableRuntimeIdOnMachine(next, userId));
  };

  const effectiveRuntimeId =
    selectedRuntimeId ||
    firstUsableRuntimeIdOnMachine(selectedMachine, userId);
  const selectedRuntime = runtimes.find((r) => r.id === effectiveRuntimeId) ?? null;
  const selectedRuntimeOnline =
    !!selectedRuntime && deriveRuntimeHealth(selectedRuntime, Date.now()) === "online";
  const hasUsableRuntime = runtimes.some((r) => isRuntimeUsableForUser(r, userId));
  const selectedRuntimeLocked =
    selectedRuntime != null &&
    !isRuntimeUsableForUser(selectedRuntime, userId);

  const handleCreate = async () => {
    if (!selectedRuntime || creating || selectedRuntimeLocked) return;
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
      onClose();
    } catch (err) {
      showErrorToast(err instanceof Error ? err.message : "Failed to create agent");
    } finally {
      setCreating(false);
    }
  };

  const handleCancel = async () => {
    if (creating) return;
    try {
      // Best-effort dismiss so the card leaves prepared state; ignore failures.
      await api.dismissAgentActionCard(card.id);
    } catch {
      // non-fatal — user still closes the dialog
    }
    onClose();
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
              <DialogTitle className="break-words text-lg font-semibold tracking-tight sm:text-xl">{t(($) => $.windy.create_title, { name: cardName })}</DialogTitle>
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

          {!hasUsableRuntime && !runtimesLoading ? (
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
          <Button type="button" variant="outline" onClick={handleCancel} disabled={creating} className="w-full sm:w-auto">
            {t(($) => $.windy.cancel)}
          </Button>
          <Button
            type="button"
            onClick={handleCreate}
            disabled={
              !selectedRuntime ||
              creating ||
              !hasUsableRuntime ||
              selectedRuntimeLocked ||
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
