"use client";

import { useMemo, useRef, useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { RuntimeConfigFields } from "./runtime-config-fields";
import {
  firstRuntimeMachine,
  firstRuntimeIdOnMachine,
  machineForRuntime,
} from "./computer-picker-utils";
import { isRuntimeUsableForUser } from "./runtime-usability";
import { InstructionsEditor } from "./instructions-editor";
import { SkillMultiSelect } from "./skill-multi-select";
import { AvatarPicker, type AvatarPickerSelection } from "./avatar-picker";
import { api } from "@multica/core/api";
import {
  AGENT_DESCRIPTION_MAX_LENGTH,
} from "@multica/core/agents";
import { useWorkspaceId } from "@multica/core/hooks";
import { resolveActorDisplayName } from "@multica/core/identity";
import { workspaceKeys } from "@multica/core/workspace/queries";
import type {
  Agent,
  AgentCreationProposal,
  AgentAvatarSelection,
  RuntimeDevice,
  MemberWithUser,
  CreateAgentRequest,
  AgentCreationDraft,
} from "@multica/core/types";
import { isImeComposing } from "@multica/core/utils";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogDescription,
} from "@multica/ui/components/ui/dialog";
import { Button } from "@multica/ui/components/ui/button";
import { Input } from "@multica/ui/components/ui/input";
import { Label } from "@multica/ui/components/ui/label";
import { toast } from "sonner";
import { showErrorToast } from "@multica/ui/lib/error-toast";
import { CharCounter } from "./char-counter";
import { randomAgentAvatarPresetUrl } from "./avatar-preset";
import { buildRuntimeMachines } from "../../runtimes/components/runtime-machines";
import { useT } from "../../i18n";

const AGENT_NAME_MAX_LENGTH = 32;
const AGENT_NAME_PATTERN = /^[a-z0-9]+(?:-[a-z0-9]+)*$/;

function duplicateName(name: string): string {
  const suffix = "-copy";
  const base = name.trim().toLowerCase().slice(0, AGENT_NAME_MAX_LENGTH - suffix.length);
  return `${base.replace(/-+$/u, "")}${suffix}`;
}

function validateAgentName(name: string, invalidMessage: string): string | null {
  const value = name.trim();
  if (!value || value.length > AGENT_NAME_MAX_LENGTH || !AGENT_NAME_PATTERN.test(value)) {
    return invalidMessage;
  }
  return null;
}

export function CreateAgentDialog({
  runtimes,
  runtimesLoading,
  members,
  currentUserId,
  template,
  draft,
  proposal,
  prefill,
  defaultMachineId = null,
  onClose,
  onCreate,
}: {
  runtimes: RuntimeDevice[];
  runtimesLoading?: boolean;
  members: MemberWithUser[];
  currentUserId: string | null;
  // When provided, the dialog opens in "Duplicate" mode: the visible
  // fields (name / description / runtime / visibility / model) are
  // pre-populated from this agent, and the hidden fields
  // (instructions / custom_args / custom_env / max_concurrent_tasks)
  // are forwarded to the create call so the new agent is a true clone.
  // Skills are copied separately by the caller after createAgent
  // succeeds — they're not part of CreateAgentRequest.
  template?: Agent | null;
  // Research / legacy seed only. Proposal commit uses canonical Message state.
  draft?: AgentCreationDraft | null;
  /** Canonical agent:create Proposal — prefills proposal fields and binds action_message_id. */
  proposal?: AgentCreationProposal | null;
  /**
   * Lightweight prefill when there is no draft/proposal/template (e.g. Notes
   * Assistant). `lockIdentity` keeps name/description/instructions fixed so
   * the human only picks Computer + runtime (+ model).
   */
  prefill?: {
    name: string;
    description?: string;
    instructions?: string;
    model?: string;
    lockIdentity?: boolean;
  } | null;
  /** Prefer this group as home when opening on「仅本群」(channel context). */
  defaultHomeChannelId?: string | null;
  /** Prefill computer (machine id from buildRuntimeMachines). */
  defaultMachineId?: string | null;
  onClose: () => void;
  // Returns the created Agent so the dialog can run a follow-up
  // setAgentSkills with the IDs the user picked in the form. Pre-skill-
  // section callers can keep returning `void`; the dialog tolerates a
  // falsy return (no follow-up runs).
  onCreate: (data: CreateAgentRequest) => Promise<Agent | void>;
}) {
  const { t } = useT("agents");
  const isDuplicate = !!template;
  const creationProposal = proposal && !isDuplicate ? proposal : null;
  const isProposal = !!creationProposal;
  const isDraft = !!draft && !isDuplicate && !isProposal;
  const identityLocked = Boolean(prefill?.lockIdentity) && !isDuplicate && !isProposal && !isDraft;
  const queryClient = useQueryClient();
  const wsId = useWorkspaceId();
  // Agent creation establishes the permanent name. The initial display
  // name matches it and remains editable later from Profile.
  const [name, setName] = useState(
    template
      ? duplicateName(template.name)
      : creationProposal?.name ?? draft?.name ?? prefill?.name ?? "",
  );
  const [description, setDescription] = useState(
    template?.description ??
      creationProposal?.description ??
      draft?.description ??
      prefill?.description ??
      "",
  );
  const [model, setModel] = useState(template?.model ?? prefill?.model ?? "");
  const [thinkingLevel, setThinkingLevel] = useState(template?.thinking_level ?? "");
  const [instructions, setInstructions] = useState(
    template?.instructions ?? draft?.instructions ?? prefill?.instructions ?? "",
  );
  // #599: never submit draft.avatar_url as a raw client URL. Preview it when
  // present; create with draft_id lets the server apply it as assigned. User
  // choices go through avatar_selection (picked preset or uploaded file).
  // Manual/proposal create seeds a random system face so a built-in
  // preset shows immediately; clear re-seeds another random face (and
  // overrides draft_id face application when the preview came from a draft).
  const seededAvatarRef = useRef<{
    preview: string | null;
    selection: AgentAvatarSelection | null;
  } | null>(null);
  if (seededAvatarRef.current === null) {
    const draftUrl = draft?.avatar_url?.trim() || null;
    const templateUrl = template?.avatar_url?.trim() || null;
    if (draftUrl) {
      seededAvatarRef.current = { preview: draftUrl, selection: null };
    } else if (templateUrl) {
      // Duplicate keeps the source face as preview only; server assigns a
      // fresh durable face unless the user explicitly re-picks / uploads.
      seededAvatarRef.current = { preview: templateUrl, selection: null };
    } else {
      const presetUrl = randomAgentAvatarPresetUrl();
      seededAvatarRef.current = {
        preview: presetUrl,
        selection: { kind: "picked", preset_url: presetUrl },
      };
    }
  }
  const [avatarPreviewUrl, setAvatarPreviewUrl] = useState<string | null>(
    () => seededAvatarRef.current!.preview,
  );
  // Never rendered — only read at submit time — so a ref avoids a redraw
  // on every avatar change.
  const avatarSelectionRef = useRef<AgentAvatarSelection | null>(
    seededAvatarRef.current!.selection,
  );
  const handleAvatarChange = (selection: AvatarPickerSelection | null) => {
    if (selection) {
      setAvatarPreviewUrl(selection.previewUrl);
      avatarSelectionRef.current =
        selection.kind === "uploaded"
          ? { kind: "uploaded", attachment_id: selection.attachmentId }
          : { kind: "picked", preset_url: selection.presetUrl };
      return;
    }
    const presetUrl = randomAgentAvatarPresetUrl();
    setAvatarPreviewUrl(presetUrl);
    avatarSelectionRef.current = { kind: "picked", preset_url: presetUrl };
  };
  const [selectedSkillIds, setSelectedSkillIds] = useState<Set<string>>(
    () => new Set(template?.skills.map((s) => s.id) ?? []),
  );
  const [creating, setCreating] = useState(false);

  // Computer → runtime selection. A Proposal's preferred Computer is only a
  // default: if it is unavailable, leave the selector blank so the human must
  // explicitly correct it rather than silently falling back to another machine.
  const initialMachines = buildRuntimeMachines(runtimes, {
    now: Date.now(),
    currentUserId,
  });
  const [selectedMachineId, setSelectedMachineId] = useState(() => {
    const templateRuntime = template?.runtime_id
      ? runtimes.find((r) => r.id === template.runtime_id)
      : undefined;
    if (
      templateRuntime &&
      isRuntimeUsableForUser(templateRuntime, currentUserId)
    ) {
      return machineForRuntime(templateRuntime, initialMachines)?.id ?? "";
    }
    if (defaultMachineId && initialMachines.some((m) => m.id === defaultMachineId)) {
      return defaultMachineId;
    }
    return "";
  });
  const [selectedRuntimeId, setSelectedRuntimeId] = useState(() => {
    const templateRuntime = template?.runtime_id
      ? runtimes.find((r) => r.id === template.runtime_id)
      : undefined;
    if (
      templateRuntime &&
      isRuntimeUsableForUser(templateRuntime, currentUserId)
    ) {
      return templateRuntime.id;
    }
    return "";
  });

  const machines = useMemo(
    () => buildRuntimeMachines(runtimes, { now: Date.now(), currentUserId }),
    [runtimes, currentUserId],
  );
  const preferredMachine = creationProposal?.preferred_computer?.trim()
    ? machines.find(
        (machine) =>
          machine.id === creationProposal.preferred_computer ||
          machine.daemonId === creationProposal.preferred_computer ||
          machine.title === creationProposal.preferred_computer ||
          machine.runtimes.some((runtime) => runtime.id === creationProposal.preferred_computer),
      ) ?? null
    : null;
  const effectiveMachineId =
    selectedMachineId ||
    (creationProposal?.preferred_computer
      ? preferredMachine?.id ?? ""
      : firstRuntimeMachine(machines, currentUserId)?.id || "") ||
    "";
  const selectedMachine =
    machines.find((m) => m.id === effectiveMachineId) ?? null;
  // Runtime dropdown only lists providers on the chosen computer —
  // never other machines' runtimes.
  const machineRuntimes = selectedMachine?.runtimes ?? [];
  const effectiveRuntimeId =
    selectedRuntimeId ||
    firstRuntimeIdOnMachine(selectedMachine, currentUserId);

  const handleMachineSelect = (machineId: string) => {
    if (machineId === effectiveMachineId) return;
    setSelectedMachineId(machineId);
    const next = machines.find((m) => m.id === machineId) ?? null;
    setSelectedRuntimeId(firstRuntimeIdOnMachine(next, currentUserId));
    setModel("");
    setThinkingLevel("");
  };

  const handleRuntimeSelect = (runtimeId: string) => {
    if (runtimeId === effectiveRuntimeId) return;
    setSelectedRuntimeId(runtimeId);
    setModel("");
    setThinkingLevel("");
  };

  const selectedRuntime = runtimes.find((d) => d.id === effectiveRuntimeId) ?? null;
  const nameError = validateAgentName(
    name,
    t(($) => $.create_dialog.name_invalid),
  );
  const handleSubmit = async () => {
    if (nameError || !selectedRuntime) return;
    const trimmedModel = model.trim();
    if (!trimmedModel) {
      showErrorToast(t(($) => $.model_dropdown.select_required));
      return;
    }
    setCreating(true);

    try {
      const trimmedInstructions = instructions.trim();
      const agentName = name.trim();
      const data: CreateAgentRequest = {
        name: agentName,
        description: description.trim(),
        runtime_id: selectedRuntime.id,
        model: trimmedModel,
        thinking_level: thinkingLevel || undefined,
        instructions: trimmedInstructions || undefined,
        avatar_selection: avatarSelectionRef.current ?? undefined,
        // Proposal commits use the canonical Message identity only.
        ...(creationProposal
          ? { action_message_id: creationProposal.message_id }
          : draft?.id
            ? { draft_id: draft.id }
            : {}),
      };
      if (template) {
        // Duplicate path: forward the hidden config fields the source
        // agent had so the clone is functional out of the box (args /
        // concurrency). Skills flow through the dialog form. As of
        // MUL-2600 the agent resource shape no longer carries
        // custom_env values, so duplication cannot copy env at all —
        // the user has to re-set env on the clone via the env tab
        // (which now goes through the audited `/env` endpoint). The
        // dialog's create call still accepts custom_env at create
        // time, but the source values aren't available here.
        if (template.custom_args.length) data.custom_args = template.custom_args;
        if (template.max_concurrent_tasks) {
          data.max_concurrent_tasks = template.max_concurrent_tasks;
        }
      }
      const createdAgent = await onCreate(data);
      // Follow-up: attach selected skills to the newly created agent.
      // onCreate returns the created Agent for this path; if the caller
      // doesn't return it we fall back to skipping (preserves
      // backward compatibility with non-skill-aware callers).
      if (createdAgent && selectedSkillIds.size > 0) {
        try {
          await api.setAgentSkills(createdAgent.id, {
            skill_ids: [...selectedSkillIds],
          });
          if (wsId) {
            queryClient.invalidateQueries({ queryKey: workspaceKeys.agents(wsId) });
          }
        } catch (skillErr) {
          // Non-fatal: agent exists, skills can be added on the detail
          // page. Surface as a warning toast so the user knows.
          toast.warning(
            t(($) => $.create_dialog.skill_attach_failed_toast, {
              error:
                skillErr instanceof Error ? skillErr.message : "unknown error",
            }),
          );
        }
      }
      onClose();
    } catch (err) {
      showErrorToast(err instanceof Error ? err.message : t(($) => $.create_dialog.create_failed_toast));
      setCreating(false);
    }
  };

  const headerTitle = isDuplicate
    ? t(($) => $.create_dialog.title_duplicate)
    : isProposal || isDraft
      ? t(($) => $.windy.create_agent)
      : t(($) => $.create_dialog.title_create);

  return (
    <Dialog open onOpenChange={(v) => { if (!v) onClose(); }}>
      <DialogContent className="p-0 gap-0 flex flex-col overflow-hidden !top-1/2 !left-1/2 !-translate-x-1/2 !-translate-y-1/2 !w-full !max-w-2xl !h-[85vh]">
        <DialogHeader className="border-b px-5 py-3 space-y-0">
          <DialogTitle className="text-base font-semibold">{headerTitle}</DialogTitle>
          {isDuplicate && template && (
            <DialogDescription className="mt-1 text-xs">
              {t(($) => $.create_dialog.description_duplicate, { name: resolveActorDisplayName(template, template.id) })}
            </DialogDescription>
          )}
          {!isDuplicate && !isDraft && !isProposal && !identityLocked && (
            <DialogDescription className="mt-1 text-xs">
              {t(($) => $.create_dialog.description_create)}
            </DialogDescription>
          )}
          {identityLocked && (
            <DialogDescription className="mt-1 text-xs">
              {t(($) => $.create_dialog.description_identity_locked)}
            </DialogDescription>
          )}
          {(isProposal || isDraft) && (
            <DialogDescription className="mt-1 text-xs">
              {t(($) => $.windy.draft_description)}
            </DialogDescription>
          )}
        </DialogHeader>

        <div className="flex-1 overflow-y-auto p-5">
          <div className="space-y-4 min-w-0">
            {/* Identity row: avatar (left) + permanent name & description stack
                (right). The avatar visually anchors the identity of
                what the user is creating; pairing it with the name
                field reads as "this is the agent's face + name",
                same shape as detail-page header so the affordance is
                instantly familiar. */}
            <div className="flex items-start gap-4">
              <div className={identityLocked ? "pointer-events-none" : undefined}>
                <AvatarPicker
                  value={avatarPreviewUrl}
                  onChange={handleAvatarChange}
                  size={64}
                />
              </div>
              <div className="flex-1 min-w-0 space-y-3">
                <div>
                  <Label className="text-xs text-muted-foreground">{t(($) => $.create_dialog.name_label)}</Label>
                  <Input
                    autoFocus={!identityLocked}
                    type="text"
                    value={name}
                    readOnly={identityLocked}
                    onChange={(e) => {
                      if (identityLocked) return;
                      setName(e.target.value);
                    }}
                    placeholder={t(($) => $.create_dialog.name_placeholder)}
                    maxLength={AGENT_NAME_MAX_LENGTH}
                    aria-invalid={nameError ? true : undefined}
                    className="mt-1"
                    onKeyDown={(e) => {
                      if (isImeComposing(e)) return;
                      if (e.key === "Enter") handleSubmit();
                    }}
                  />
                  <p className={nameError ? "mt-1 text-xs text-destructive" : "mt-1 text-xs text-muted-foreground"}>
                    {nameError ?? t(($) => $.create_dialog.name_hint)}
                  </p>
                </div>

                <div>
                  <Label className="text-xs text-muted-foreground">{t(($) => $.create_dialog.description_label)}</Label>
                  <Input
                    type="text"
                    value={description}
                    readOnly={identityLocked}
                    onChange={(e) => {
                      if (identityLocked) return;
                      setDescription(e.target.value);
                    }}
                    placeholder={t(($) => $.create_dialog.description_placeholder)}
                    maxLength={AGENT_DESCRIPTION_MAX_LENGTH}
                    className="mt-1"
                  />
                  <div className="mt-1">
                    <CharCounter
                      length={[...description].length}
                      max={AGENT_DESCRIPTION_MAX_LENGTH}
                    />
                  </div>
                </div>
              </div>
            </div>


            <RuntimeConfigFields
              runtimes={runtimes}
              runtimesLoading={runtimesLoading}
              members={members}
              currentUserId={currentUserId}
              machineId={effectiveMachineId}
              onMachineSelect={handleMachineSelect}
              machineRuntimes={machineRuntimes}
              runtimeId={effectiveRuntimeId}
              onRuntimeSelect={handleRuntimeSelect}
              model={model}
              onModelChange={(next) => {
                if (next === model) return;
                setModel(next);
                setThinkingLevel("");
              }}
              thinkingLevel={thinkingLevel}
              onThinkingChange={setThinkingLevel}
              modelRequired
            />

            {/* --- Optional sections (instructions / skills) ---
                Collapsed by default so quick-create stays fast.
                Duplicate pre-fills everything from the source agent.
                Identity-locked Notes Assistant: show instructions read-only,
                hide skills (server template owns them). */}
            {!identityLocked ? (
              <>
                <InstructionsEditor
                  value={instructions}
                  onChange={setInstructions}
                  placeholder={
                    isDuplicate
                      ? t(($) => $.create_dialog.instructions.placeholder_duplicate)
                      : t(($) => $.create_dialog.instructions.placeholder_blank)
                  }
                />
                <SkillMultiSelect
                  selectedIds={selectedSkillIds}
                  onChange={setSelectedSkillIds}
                />
              </>
            ) : instructions.trim() ? (
              <div className="rounded-lg border bg-muted/20 px-3 py-2">
                <p className="text-xs font-medium text-muted-foreground">
                  {t(($) => $.create_dialog.instructions.label)}
                </p>
                <p className="mt-1 max-h-28 overflow-y-auto whitespace-pre-wrap text-xs leading-5 text-muted-foreground">
                  {instructions}
                </p>
              </div>
            ) : null}
          </div>
        </div>

        {/* Inline footer instead of <DialogFooter>: the shipped
            DialogFooter applies `-mx-4 -mb-4` assuming a padded
            DialogContent (default `p-4`). Our DialogContent uses
            `p-0`, so those negative margins push the footer outside
            the dialog. A plain flex row anchored by `border-t` keeps
            the visual rhythm without the overflow bug. */}
        <div className="flex items-center justify-end gap-2 border-t bg-background px-5 py-3">
          <Button variant="ghost" onClick={onClose}>
            {t(($) => $.create_dialog.cancel)}
          </Button>
          <Button
            onClick={handleSubmit}
            disabled={
              creating ||
              !!nameError ||
              !selectedRuntime ||
              !model.trim()
            }
            title={
              !model.trim()
                ? t(($) => $.model_dropdown.select_required)
                : undefined
            }
          >
            {creating ? t(($) => $.create_dialog.creating) : t(($) => $.create_dialog.create)}
          </Button>
        </div>
      </DialogContent>
    </Dialog>
  );
}
