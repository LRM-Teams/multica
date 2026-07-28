"use client";

import * as React from "react";
import { Bot, CheckCircle2, Globe, Hash, Lock, Loader2, X } from "lucide-react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import { api } from "@multica/core/api";
import {
  VISIBILITY_DESCRIPTION,
  VISIBILITY_LABEL,
  VISIBILITY_OPTIONS,
} from "@multica/core/agents";
import { channelsOptions, channelKeys } from "@multica/core/channels";
import { useWorkspaceId } from "@multica/core/hooks";
import { useAuthStore } from "@multica/core/auth";
import { memberListOptions, workspaceKeys } from "@multica/core/workspace/queries";
import { runtimeListOptions } from "@multica/core/runtimes";
import type {
  Agent,
  AgentCreationDraft,
  AgentVisibility,
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
import { Label } from "@multica/ui/components/ui/label";
import { RuntimePicker, isRuntimeUsableForUser } from "../agents/components/runtime-picker";
import { ModelDropdown } from "../agents/components/model-dropdown";
import { ThinkingDropdown } from "../agents/components/thinking-dropdown";
import {
  HomeChannelBindPanel,
  type HomeChannelMode,
} from "../agents/components/home-channel-bind-panel";
import { cn } from "@multica/ui/lib/utils";
import { useT } from "../i18n";
import { listParam, parseWindyCreateAgentURL } from "./windy-create-agent-link-utils";

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

function seedVisibility(draft: AgentCreationDraft): AgentVisibility {
  const seed = draft.visibility;
  return seed === "channel" || seed === "private" || seed === "workspace"
    ? seed
    : "workspace";
}
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
    if (!url || creatingDraft || createdAgentName) return;
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
            visibility: (() => {
              const v = url.searchParams.get("visibility");
              if (v === "workspace" || v === "channel" || v === "private") return v;
              return "private";
            })(),
            project_id: url.searchParams.get("project_id") || null,
            channel_id: url.searchParams.get("channel_id") || null,
            can_execute_code: url.searchParams.get("can_execute_code") === "true",
            suggested_channels: listParam(url, "suggested_channel"),
            recommended_tools: listParam(url, "tool"),
          });
      setDraft(createdDraft);
    } catch (err) {
      toast.error(err instanceof Error ? err.message : "Failed to load agent draft");
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
  // Hire card fields are independent (visibility/home vs runtime/model);
  // reducer would obscure pickVisibility/submit guards without reducing churn.
  // react-doctor-disable-next-line react-doctor/prefer-useReducer
}) {
  const { t } = useT("agents");
  const { t: tModals } = useT("modals");
  const wsId = useWorkspaceId();
  const currentUser = useAuthStore((s) => s.user);
  const qc = useQueryClient();
  const { data: runtimes = [], isLoading: runtimesLoading } = useQuery(runtimeListOptions(wsId));
  const { data: members = [] } = useQuery(memberListOptions(wsId));
  const { data: channels = [], isLoading: channelsLoading } = useQuery(channelsOptions(wsId));
  const groups = React.useMemo(
    () => channels.filter((c) => c.kind === "group" && !c.archived_at),
    [channels],
  );
  const hasGroups = groups.length > 0;

  const [visibility, setVisibility] = React.useState<AgentVisibility>(() =>
    seedVisibility(draft),
  );
  const [homeMode, setHomeMode] = React.useState<HomeChannelMode>(() => {
    if (draft.channel_id) return "existing";
    return "existing";
  });
  const [homeChannelId, setHomeChannelId] = React.useState<string | null>(() => {
    if (draft.channel_id) return draft.channel_id;
    return null;
  });
  const [newChannelName, setNewChannelName] = React.useState(() => draft.name || "");
  const [homeInvalid, setHomeInvalid] = React.useState(false);
  const [selectedRuntimeId, setSelectedRuntimeId] = React.useState(() => {
    const firstUsable = runtimes.find((r) => isRuntimeUsableForUser(r, currentUser?.id ?? null));
    return firstUsable?.id ?? "";
  });
  const [model, setModel] = React.useState("");
  const [thinkingLevel, setThinkingLevel] = React.useState("");
  const [creating, setCreating] = React.useState(false);

  const handleOpenChange = (open: boolean) => {
    if (!open) onClose();
  };

  const firstUsableRuntimeId = runtimes.find((r) => isRuntimeUsableForUser(r, currentUser?.id ?? null))?.id ?? "";
  const effectiveRuntimeId = selectedRuntimeId || firstUsableRuntimeId;
  const selectedRuntime = runtimes.find((r) => r.id === effectiveRuntimeId) ?? null;
  const hasUsableRuntime = runtimes.some((r) => isRuntimeUsableForUser(r, currentUser?.id ?? null));

  const pickVisibility = (next: AgentVisibility) => {
    setVisibility(next);
    setHomeInvalid(false);
    if (next === "channel") {
      if (!hasGroups) {
        setHomeMode("new");
        if (!newChannelName.trim()) setNewChannelName(draft.name || "");
      } else {
        setHomeMode((prev) => prev);
        setHomeChannelId((prev) => {
          if (prev) return prev;
          if (draft.channel_id) return draft.channel_id;
          return groups[0]?.id ?? null;
        });
      }
    } else {
      setHomeChannelId(null);
    }
  };

  const handleCreate = async () => {
    if (!selectedRuntime || creating) return;
    const useNewHome = visibility === "channel" && (homeMode === "new" || !hasGroups);
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
        await qc.invalidateQueries({ queryKey: channelKeys.list(wsId) });
      }
      const payload: CreateAgentRequest = {
        display_name: draft.name,
        description: draft.description,
        instructions: draft.instructions,
        // #599: avatar_selection is intentionally omitted — the server
        // resolves the draft's suggested avatar itself via draft_id and
        // records it as `assigned`. draft.avatar_url is a preview-only
        // suggestion string; it must never be resubmitted as a raw URL.
        visibility,
        home_channel_id: visibility === "channel" ? resolvedHomeId : undefined,
        runtime_id: selectedRuntime.id,
        model: model.trim() || undefined,
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
      toast.error(err instanceof Error ? err.message : "Failed to create agent");
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
            <div className="mb-3 min-w-0">
              <p className="break-words text-sm font-semibold">{draft.description || draft.name}</p>
              <p className="mt-0.5 text-xs text-muted-foreground">{t(($) => $.windy.generated_hint)}</p>
            </div>
            {draft.instructions && (
              <p className="max-h-40 overflow-y-auto whitespace-pre-wrap break-words rounded-md border bg-muted/25 px-3 py-2 text-xs leading-5 text-muted-foreground">
                {draft.instructions}
              </p>
            )}
          </div>

          <div>
            <Label className="text-xs text-muted-foreground">
              {t(($) => $.create_dialog.visibility_label)}
            </Label>
            <div
              className="mt-1.5 flex flex-col gap-2"
              role="radiogroup"
              aria-label={t(($) => $.create_dialog.visibility_label)}
            >
              {VISIBILITY_OPTIONS.map((option) => {
                const selected = visibility === option;
                return (
                  <button
                    key={option}
                    type="button"
                    role="radio"
                    aria-checked={selected}
                    onClick={() => pickVisibility(option)}
                    className={cn(
                      "flex items-start gap-2.5 rounded-lg border px-3 py-2.5 text-sm transition-colors",
                      selected
                        ? "border-primary bg-primary/5"
                        : "border-border hover:bg-muted",
                    )}
                  >
                    <span
                      aria-hidden
                      className={cn(
                        "mt-0.5 flex h-4 w-4 shrink-0 items-center justify-center rounded-full border-2",
                        selected ? "border-primary" : "border-muted-foreground/50",
                      )}
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
                                setNewChannelName(draft.name || "");
                              }
                            }}
                            existingChannelId={homeChannelId}
                            onExistingChannelChange={(id) => {
                              setHomeChannelId(id);
                              setHomeInvalid(false);
                            }}
                            newChannelName={newChannelName}
                            onNewChannelNameChange={(name) => {
                              setNewChannelName(name);
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

          {!hasUsableRuntime && !runtimesLoading ? (
            <div className="rounded-lg border border-dashed bg-muted/20 px-4 py-6 text-center text-sm text-muted-foreground">
              {t(($) => $.windy.runtime_required)}
            </div>
          ) : (
            <div className="grid gap-4 sm:grid-cols-2">
              <div className="sm:col-span-2">
                <RuntimePicker
                  runtimes={runtimes}
                  runtimesLoading={runtimesLoading}
                  members={members}
                  currentUserId={currentUser?.id ?? null}
                  selectedRuntimeId={effectiveRuntimeId}
                  onSelect={setSelectedRuntimeId}
                />
              </div>
              <ModelDropdown
                runtimeId={effectiveRuntimeId || null}
                runtimeOnline={selectedRuntime?.status === "online"}
                value={model}
                onChange={setModel}
                disabled={!effectiveRuntimeId}
              />
              <ThinkingDropdown
                runtimeId={effectiveRuntimeId || null}
                runtimeOnline={selectedRuntime?.status === "online"}
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
          <Button type="button" onClick={handleCreate} disabled={!selectedRuntime || creating || !hasUsableRuntime} className="w-full sm:w-auto">
            {creating ? <Loader2 className="size-4 animate-spin" /> : null}
            {t(($) => $.windy.create_agent)}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
