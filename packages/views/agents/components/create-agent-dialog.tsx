"use client";

import { useMemo, useRef, useState } from "react";
import { Globe, Hash, Lock } from "lucide-react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { ModelDropdown } from "./model-dropdown";
import { ThinkingDropdown } from "./thinking-dropdown";
import { RuntimePicker, isRuntimeUsableForUser } from "./runtime-picker";
import { InstructionsEditor } from "./instructions-editor";
import { SkillMultiSelect } from "./skill-multi-select";
import { AvatarPicker, type AvatarPickerSelection } from "./avatar-picker";
import { api } from "@multica/core/api";
import {
  AGENT_DESCRIPTION_MAX_LENGTH,
  VISIBILITY_DESCRIPTION,
  VISIBILITY_LABEL,
  VISIBILITY_OPTIONS,
} from "@multica/core/agents";
import { channelKeys, channelsOptions } from "@multica/core/channels";
import { useWorkspaceId } from "@multica/core/hooks";
import { resolveActorDisplayName } from "@multica/core/identity";
import { workspaceKeys } from "@multica/core/workspace/queries";
import type {
  Agent,
  AgentAvatarSelection,
  AgentVisibility,
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
import { CharCounter } from "./char-counter";
import {
  HomeChannelBindPanel,
  type HomeChannelMode,
} from "./home-channel-bind-panel";
import { randomPickedAvatarSelection } from "./avatar-preset";
import { useT } from "../../i18n";

function VisibilityOptionIcon({
  value,
  className,
}: {
  value: AgentVisibility;
  className?: string;
}) {
  if (value === "private") return <Lock className={className} />;
  if (value === "channel") return <Hash className={className} />;
  return <Globe className={className} />;
}

export function CreateAgentDialog({
  runtimes,
  runtimesLoading,
  members,
  currentUserId,
  template,
  draft,
  defaultHomeChannelId,
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
  // When provided by Wendy, the dialog opens with a generated role draft
  // and marks that draft as used after the agent is created.
  draft?: AgentCreationDraft | null;
  /** Prefer this group as home when opening on「仅本群」(channel context). */
  defaultHomeChannelId?: string | null;
  onClose: () => void;
  // Returns the created Agent so the dialog can run a follow-up
  // setAgentSkills with the IDs the user picked in the form. Pre-skill-
  // section callers can keep returning `void`; the dialog tolerates a
  // falsy return (no follow-up runs).
  onCreate: (data: CreateAgentRequest) => Promise<Agent | void>;
}) {
  const { t } = useT("agents");
  const isDuplicate = !!template;
  const isDraft = !!draft && !isDuplicate;
  const queryClient = useQueryClient();
  const wsId = useWorkspaceId();
  const { data: channels = [], isLoading: channelsLoading } = useQuery(
    channelsOptions(wsId),
  );
  const groups = useMemo(
    () => channels.filter((c) => c.kind === "group" && !c.archived_at),
    [channels],
  );
  const hasGroups = groups.length > 0;

  // Display-name defaults: duplicate uses "<original> copy". Manual-create starts blank.
  const [name, setName] = useState(
    template
      ? `${resolveActorDisplayName(template, template.id)}${t(($) => $.create_dialog.duplicate_copy_suffix)}`
      : draft?.name ?? "",
  );
  const [description, setDescription] = useState(template?.description ?? draft?.description ?? "");
  const [visibility, setVisibility] = useState<AgentVisibility>(() => {
    const seed = template?.visibility ?? draft?.visibility ?? "workspace";
    // Older drafts/templates may only carry workspace|private; never invent channel.
    return seed === "channel" || seed === "private" || seed === "workspace"
      ? seed
      : "workspace";
  });
  const [homeMode, setHomeMode] = useState<HomeChannelMode>("existing");
  const [homeChannelId, setHomeChannelId] = useState<string | null>(() => {
    if (template?.visibility === "channel" && template.home_channel_id) {
      return template.home_channel_id;
    }
    if (draft?.channel_id) return draft.channel_id;
    return defaultHomeChannelId ?? null;
  });
  const [newChannelName, setNewChannelName] = useState(
    () => draft?.name ?? template?.display_name ?? "",
  );
  const [homeInvalid, setHomeInvalid] = useState(false);
  const [model, setModel] = useState(template?.model ?? "");
  const [thinkingLevel, setThinkingLevel] = useState(template?.thinking_level ?? "");
  const [instructions, setInstructions] = useState(template?.instructions ?? draft?.instructions ?? "");
  // #599: never submit draft.avatar_url as a raw client URL. Preview it when
  // present; create with draft_id lets the server apply it as assigned. User
  // uploads go through avatar_selection; clearing a draft preview sends a
  // random picked preset so create does not re-apply the draft face.
  const [avatarPreviewUrl, setAvatarPreviewUrl] = useState<string | null>(
    () => (draft?.avatar_url?.trim() ? draft.avatar_url : null),
  );
  // Never rendered — only read at submit time — so a ref avoids a redraw
  // on every avatar change.
  const avatarSelectionRef = useRef<AgentAvatarSelection | null>(null);
  const draftAvatarUrl = draft?.avatar_url?.trim() || null;
  const handleAvatarChange = (selection: AvatarPickerSelection | null) => {
    if (selection) {
      setAvatarPreviewUrl(selection.previewUrl);
      avatarSelectionRef.current = { kind: "uploaded", attachment_id: selection.attachmentId };
      return;
    }
    setAvatarPreviewUrl(null);
    // Clearing a draft-seeded face must override draft_id avatar application.
    avatarSelectionRef.current = draftAvatarUrl
      ? randomPickedAvatarSelection()
      : null;
  };
  const [selectedSkillIds, setSelectedSkillIds] = useState<Set<string>>(
    () => new Set(template?.skills.map((s) => s.id) ?? []),
  );
  const [creating, setCreating] = useState(false);

  // Duplicate-mode pre-fill: clone lands on the source agent's runtime so
  // the user doesn't have to re-pick. Skipped when that runtime is now
  // locked for the caller (Create would 403). Empty fallback hands the
  // job to RuntimePicker — it owns filter state, so it's the only place
  // that knows which runtimes are visible right now.
  const [selectedRuntimeId, setSelectedRuntimeId] = useState(() => {
    const templateRuntime = template?.runtime_id
      ? runtimes.find((r) => r.id === template.runtime_id)
      : undefined;
    if (templateRuntime && isRuntimeUsableForUser(templateRuntime, currentUserId)) {
      return templateRuntime.id;
    }
    const usableRuntime = runtimes.find((r) => isRuntimeUsableForUser(r, currentUserId));
    return usableRuntime?.id ?? "";
  });

  const selectedRuntime = runtimes.find((d) => d.id === selectedRuntimeId) ?? null;
  // Defense-in-depth: even if a locked runtime somehow ends up selected
  // (e.g. duplicate of an agent whose template runtime is now locked, and
  // the workspace has no usable fallback), gate Create on it so we don't
  // submit a request the backend will reject with 403.
  const selectedRuntimeLocked =
    selectedRuntime != null &&
    !isRuntimeUsableForUser(selectedRuntime, currentUserId);

  const pickVisibility = (next: AgentVisibility) => {
    setVisibility(next);
    setHomeInvalid(false);
    if (next === "channel") {
      if (!channelsLoading && !hasGroups) {
        setHomeMode("new");
        if (!newChannelName.trim()) {
          setNewChannelName(name.trim() || draft?.name || "");
        }
      } else {
        setHomeChannelId((prev) => {
          // Keep an existing/default bind while the channel list is still
          // loading — never wipe home just because groups[] is empty yet.
          if (prev) return prev;
          if (defaultHomeChannelId) return defaultHomeChannelId;
          return groups[0]?.id ?? null;
        });
      }
    } else {
      setHomeChannelId(null);
    }
  };

  const handleSubmit = async () => {
    if (!name.trim() || !selectedRuntime || selectedRuntimeLocked) return;
    const useNewHome = visibility === "channel" && (homeMode === "new" || (!channelsLoading && !hasGroups));
    // Channel without home is an explicit hard stop — never silently
    // rewrite to private/workspace (LRM-238 / LRM-371 AC).
    if (visibility === "channel") {
      if (useNewHome) {
        if (!newChannelName.trim()) {
          setHomeInvalid(true);
          toast.error(t(($) => $.visibility_bind.new_channel_name_required));
          return;
        }
      } else if (!homeChannelId) {
        setHomeInvalid(true);
        toast.error(t(($) => $.visibility_bind.home_required));
        return;
      }
    }
    setCreating(true);

    try {
      let resolvedHomeId = homeChannelId;
      if (visibility === "channel" && useNewHome) {
        const createdChannel = await api.createChannel({
          name: newChannelName.trim(),
        });
        resolvedHomeId = createdChannel.id;
        await queryClient.invalidateQueries({ queryKey: channelKeys.list(wsId) });
      }
      const trimmedInstructions = instructions.trim();
      const data: CreateAgentRequest = {
        display_name: name.trim(),
        description: description.trim(),
        runtime_id: selectedRuntime.id,
        visibility,
        home_channel_id:
          visibility === "channel" ? resolvedHomeId : undefined,
        model: model.trim() || undefined,
        thinking_level: thinkingLevel || undefined,
        instructions: trimmedInstructions || undefined,
        avatar_selection: avatarSelectionRef.current ?? undefined,
        draft_id: draft?.id,
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
      toast.error(err instanceof Error ? err.message : t(($) => $.create_dialog.create_failed_toast));
      setCreating(false);
    }
  };

  const headerTitle = isDuplicate
    ? t(($) => $.create_dialog.title_duplicate)
    : isDraft
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
          {!isDuplicate && !isDraft && (
            <DialogDescription className="mt-1 text-xs">
              {t(($) => $.create_dialog.description_create)}
            </DialogDescription>
          )}
          {isDraft && draft && (
            <DialogDescription className="mt-1 text-xs">
              {t(($) => $.windy.draft_description)}
            </DialogDescription>
          )}
        </DialogHeader>

        <div className="flex-1 overflow-y-auto p-5">
          <div className="space-y-4 min-w-0">
            {/* Identity row: avatar (left) + display name & description stack
                (right). The avatar visually anchors the identity of
                what the user is creating; pairing it with the display-name
                field reads as "this is the agent's face + name",
                same shape as detail-page header so the affordance is
                instantly familiar. */}
            <div className="flex items-start gap-4">
              <AvatarPicker value={avatarPreviewUrl} onChange={handleAvatarChange} size={64} />
              <div className="flex-1 min-w-0 space-y-3">
                <div>
                  <Label className="text-xs text-muted-foreground">{t(($) => $.create_dialog.display_name_label)}</Label>
                  <Input
                    autoFocus
                    type="text"
                    value={name}
                    onChange={(e) => setName(e.target.value)}
                    placeholder={t(($) => $.create_dialog.name_placeholder)}
                    className="mt-1"
                    onKeyDown={(e) => {
                      if (isImeComposing(e)) return;
                      if (e.key === "Enter") handleSubmit();
                    }}
                  />
                  <p className="mt-1 text-xs text-muted-foreground">
                    {t(($) => $.create_dialog.handle_auto_hint)}
                  </p>
                </div>

                <div>
                  <Label className="text-xs text-muted-foreground">{t(($) => $.create_dialog.description_label)}</Label>
                  <Input
                    type="text"
                    value={description}
                    onChange={(e) => setDescription(e.target.value)}
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

            <div>
              <Label className="text-xs text-muted-foreground">{t(($) => $.create_dialog.visibility_label)}</Label>
              {/* 方案 A: vertical radio list;「仅本群」expands #home chip inline. */}
              <div className="mt-1.5 flex flex-col gap-2" role="radiogroup" aria-label={t(($) => $.create_dialog.visibility_label)}>
                {VISIBILITY_OPTIONS.map((option) => {
                  const selected = visibility === option;
                  return (
                    <button
                      key={option}
                      type="button"
                      role="radio"
                      aria-checked={selected}
                      onClick={() => pickVisibility(option)}
                      className={`flex items-start gap-2.5 rounded-lg border px-3 py-2.5 text-sm transition-colors ${
                        selected
                          ? "border-primary bg-primary/5"
                          : "border-border hover:bg-muted"
                      }`}
                    >
                      <span
                        aria-hidden
                        className={`mt-0.5 flex h-4 w-4 shrink-0 items-center justify-center rounded-full border-2 ${
                          selected ? "border-primary" : "border-muted-foreground/50"
                        }`}
                      >
                        {selected ? (
                          <span className="h-2 w-2 rounded-full bg-primary" />
                        ) : null}
                      </span>
                      <VisibilityOptionIcon
                        value={option}
                        className="mt-0.5 h-4 w-4 shrink-0 text-muted-foreground"
                      />
                      <div className="min-w-0 flex-1 text-left">
                        <div className="font-medium">{VISIBILITY_LABEL[option]}</div>
                        <div className="text-xs text-muted-foreground">
                          {VISIBILITY_DESCRIPTION[option]}
                        </div>
                        {option === "channel" && selected ? (
                          <div className="mt-2">
                            <HomeChannelBindPanel
                              mode={!channelsLoading && !hasGroups ? "new" : homeMode}
                              onModeChange={(next) => {
                                setHomeMode(next);
                                setHomeInvalid(false);
                                if (next === "new" && !newChannelName.trim()) {
                                  setNewChannelName(name.trim() || draft?.name || "");
                                }
                              }}
                              existingChannelId={homeChannelId}
                              onExistingChannelChange={(id) => {
                                setHomeChannelId(id);
                                setHomeInvalid(false);
                              }}
                              newChannelName={newChannelName}
                              onNewChannelNameChange={(nextName) => {
                                setNewChannelName(nextName);
                                setHomeInvalid(false);
                              }}
                              invalid={homeInvalid}
                              hasGroups={hasGroups || channelsLoading}
                            />
                          </div>
                        ) : null}
                      </div>
                    </button>
                  );
                })}
              </div>
              <p className="mt-2 text-xs text-muted-foreground">
                {t(($) => $.visibility_bind.not_channel_permission_hint)}
              </p>
            </div>

            <RuntimePicker
              runtimes={runtimes}
              runtimesLoading={runtimesLoading}
              members={members}
              currentUserId={currentUserId}
              selectedRuntimeId={selectedRuntimeId}
              onSelect={setSelectedRuntimeId}
            />

            <ModelDropdown
              runtimeId={selectedRuntime?.id ?? null}
              runtimeOnline={selectedRuntime?.status === "online"}
              value={model}
              onChange={(next) => {
                setModel(next);
                setThinkingLevel("");
              }}
              disabled={!selectedRuntime}
            />

            <ThinkingDropdown
              runtimeId={selectedRuntime?.id ?? null}
              runtimeOnline={selectedRuntime?.status === "online"}
              model={model}
              value={thinkingLevel}
              onChange={setThinkingLevel}
              disabled={!selectedRuntime}
            />

            {/* --- Optional sections (instructions / skills) ---
                Collapsed by default so quick-create stays fast.
                Duplicate pre-fills everything from the source agent. */}
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
              creating || !name.trim() || !selectedRuntime || selectedRuntimeLocked
            }
            title={
              selectedRuntimeLocked
                ? t(($) => $.create_dialog.runtime_private_locked_tooltip)
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
