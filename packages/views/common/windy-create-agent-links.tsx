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
  AgentAvatarSelection,
  AgentCreationDraft,
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
import { randomPickedAvatarSelection } from "../agents/components/avatar-preset";
import { buildRuntimeMachines } from "../runtimes/components/runtime-machines";
import { cn } from "@multica/ui/lib/utils";
import { useT } from "../i18n";
import { listParam, parseWindyCreateAgentURL } from "./windy-create-agent-link-utils";

export function WindyCreateAgentLink({
  href,
  children,
  className,
}: {
  href: string;
  children: React.ReactNode;
  className?: string;
}) {
  const [creatingDraft, setCreatingDraft] = React.useState(false);
  const [draft, setDraft] = React.useState<AgentCreationDraft | null>(null);
  const [createdAgentName, setCreatedAgentName] = React.useState<string | null>(null);

  const handleClick = async () => {
    const url = parseWindyCreateAgentURL(href);
    if (creatingDraft || createdAgentName) return;
    if (!url) {
      showErrorToast("Invalid Create Agent link");
      return;
    }
    const draftId = url.searchParams.get("draft_id")?.trim();
    const name = url.searchParams.get("name")?.trim() || "New Agent";
    setCreatingDraft(true);
    try {
      const createdDraft = draftId
        ? await api.getAgentDraft(draftId)
        : await api.createAgentDraft({
            name,
            description: url.searchParams.get("description")?.trim() || "",
            instructions: url.searchParams.get("instructions")?.trim() || "",
            // #599: a link's `avatar_url` query param is fully client-
            // controlled (anyone who can post a message can craft one) and
            // was getting persisted onto the draft row, then promoted to a
            // real agent as trusted `assigned` avatar via draft_id — a raw
            // URL bypass of the avatar_selection contract. Never forward it;
            // the created agent gets the server's concrete assigned default.
            //
            // No `visibility`: agent visibility was retired (#908), and a
            // link's `visibility=` param is now ignored rather than seeded
            // into the draft.
            project_id: url.searchParams.get("project_id") || null,
            channel_id: url.searchParams.get("channel_id") || null,
            can_execute_code: url.searchParams.get("can_execute_code") === "true",
            suggested_channels: listParam(url, "suggested_channel"),
            recommended_tools: listParam(url, "tool"),
          });
      setDraft(createdDraft);
    } catch (err) {
      showErrorToast(err instanceof Error ? err.message : "Failed to load agent draft");
    } finally {
      setCreatingDraft(false);
    }
  };

  return (
    <>
      <Button
        type="button"
        size="sm"
        variant="outline"
        disabled={creatingDraft || !!createdAgentName}
        onClick={handleClick}
        className={cn(
          "not-prose my-1 inline-flex max-w-full gap-2 transition-all",
          createdAgentName && "border-muted bg-muted/60 text-muted-foreground shadow-none opacity-80",
          className,
        )}
      >
        {creatingDraft ? (
          <Loader2 className="size-3.5 animate-spin" />
        ) : createdAgentName ? (
          <CheckCircle2 className="size-3.5 text-success" />
        ) : (
          <Bot className="size-3.5" />
        )}
        <span className="truncate">{createdAgentName ? `Created: ${createdAgentName}` : children}</span>
      </Button>
      {draft && (
        <InlineCreateAgentDialog
          draft={draft}
          onCreated={(name) => setCreatedAgentName(name)}
          onClose={() => setDraft(null)}
        />
      )}
    </>
  );
}

function InlineCreateAgentDialog({
  draft,
  onCreated,
  onClose,
}: {
  draft: AgentCreationDraft;
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
  const draftAvatarUrl = draft.avatar_url?.trim() || null;
  const [avatarPreviewUrl, setAvatarPreviewUrl] = React.useState<string | null>(
    () => draftAvatarUrl,
  );
  const avatarSelectionRef = React.useRef<AgentAvatarSelection | null>(null);
  const handleAvatarChange = (selection: AvatarPickerSelection | null) => {
    if (selection) {
      setAvatarPreviewUrl(selection.previewUrl);
      avatarSelectionRef.current = { kind: "uploaded", attachment_id: selection.attachmentId };
      return;
    }
    setAvatarPreviewUrl(null);
    avatarSelectionRef.current = draftAvatarUrl
      ? randomPickedAvatarSelection()
      : null;
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
  // Derived, staleness-aware health instead of the raw `status` column
  // (#10 — "runtime online status" had two divergent sources across the
  // app). Computed once so both dropdowns below agree.
  const selectedRuntimeOnline =
    !!selectedRuntime && deriveRuntimeHealth(selectedRuntime, Date.now()) === "online";
  // Workspace-level empty state (no usable runtime anywhere). Distinct from
  // selectedRuntimeLocked below — a usable runtime on another machine must
  // not keep Create enabled when the selected computer's runtime is locked.
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
        display_name: draft.name,
        description: draft.description,
        instructions: draft.instructions,
        // #599: never resubmit draft.avatar_url as a raw URL. Prefer an
        // explicit avatar_selection (user upload or clear→random picked);
        // otherwise omit so draft_id applies the draft face, or the DB
        // trigger assigns a random human preset when the draft has none.
        avatar_selection: avatarSelectionRef.current ?? undefined,
        runtime_id: selectedRuntime.id,
        model: trimmedModel,
        thinking_level: thinkingLevel || undefined,
        max_concurrent_tasks: 6,
        draft_id: draft.id,
      };
      const created = await api.createAgent(payload);
      qc.setQueryData<Agent[]>(workspaceKeys.agents(wsId), (current = []) => [...current, created]);
      qc.invalidateQueries({ queryKey: workspaceKeys.agents(wsId) });
      toast.success(t(($) => $.windy.created_toast, { name: draft.name }));
      onCreated(created.display_name || draft.name);
      onClose();
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
              <DialogTitle className="break-words text-lg font-semibold tracking-tight sm:text-xl">{t(($) => $.windy.create_title, { name: draft.name })}</DialogTitle>
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
                <p className="break-words text-sm font-semibold">{draft.description || draft.name}</p>
                <p className="mt-0.5 text-xs text-muted-foreground">{t(($) => $.windy.generated_hint)}</p>
                <p className="mt-1 text-xs text-muted-foreground">
                  {draftAvatarUrl
                    ? t(($) => $.windy.avatar_from_draft_hint)
                    : t(($) => $.windy.avatar_random_hint)}
                </p>
              </div>
            </div>
            {draft.instructions && (
              <p className="max-h-40 overflow-y-auto whitespace-pre-wrap break-words rounded-md border bg-muted/25 px-3 py-2 text-xs leading-5 text-muted-foreground">
                {draft.instructions}
              </p>
            )}
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
                  getItemLabel={(runtime) => runtime.name}
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
