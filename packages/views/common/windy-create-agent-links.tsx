"use client";

import * as React from "react";
import { Bot, CheckCircle2, Loader2, Sparkles, X } from "lucide-react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import { api } from "@multica/core/api";
import { useWorkspaceId } from "@multica/core/hooks";
import { useAuthStore } from "@multica/core/auth";
import { memberListOptions, workspaceKeys } from "@multica/core/workspace/queries";
import { runtimeListOptions } from "@multica/core/runtimes";
import type { Agent, AgentCreationDraft, CreateAgentRequest } from "@multica/core/types";
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
import { RuntimePicker, isRuntimeUsableForUser } from "../agents/components/runtime-picker";
import { ModelDropdown } from "../agents/components/model-dropdown";
import { ThinkingDropdown } from "../agents/components/thinking-dropdown";
import { cn } from "@multica/ui/lib/utils";
import { useT } from "../i18n";
import { listParam, parseWindyCreateAgentURL } from "./windy-create-agent-link-utils";
import { randomAgentAvatarUrl } from "./agent-avatar-presets";

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
  const [fallbackAvatarUrl] = React.useState(randomAgentAvatarUrl);

  const handleClick = async () => {
    const url = parseWindyCreateAgentURL(href);
    if (!url || creatingDraft || createdAgentName) return;
    const name = url.searchParams.get("name")?.trim() || "New Agent";
    setCreatingDraft(true);
    try {
      const createdDraft = await api.createAgentDraft({
        name,
        description: url.searchParams.get("description")?.trim() || "",
        instructions: url.searchParams.get("instructions")?.trim() || "",
        avatar_url: url.searchParams.get("avatar_url") || fallbackAvatarUrl,
        visibility: url.searchParams.get("visibility") === "workspace" ? "workspace" : "private",
        project_id: url.searchParams.get("project_id") || null,
        channel_id: url.searchParams.get("channel_id") || null,
        can_execute_code: url.searchParams.get("can_execute_code") === "true",
        suggested_channels: listParam(url, "suggested_channel"),
        recommended_tools: listParam(url, "tool"),
      });
      setDraft(createdDraft);
    } catch (err) {
      toast.error(err instanceof Error ? err.message : "Failed to create agent draft");
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
}) {
  const { t } = useT("agents");
  const wsId = useWorkspaceId();
  const currentUser = useAuthStore((s) => s.user);
  const qc = useQueryClient();
  const { data: runtimes = [], isLoading: runtimesLoading } = useQuery(runtimeListOptions(wsId));
  const { data: members = [] } = useQuery(memberListOptions(wsId));
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

  const handleCreate = async () => {
    if (!selectedRuntime || creating) return;
    setCreating(true);
    try {
      const payload: CreateAgentRequest = {
        display_name: draft.name,
        description: draft.description,
        instructions: draft.instructions,
        avatar_url: draft.avatar_url ?? undefined,
        visibility: draft.visibility,
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
        className="bottom-2 top-auto flex max-h-[calc(100dvh-1rem)] w-[calc(100vw-1rem)] max-w-[min(940px,calc(100vw-1rem))] translate-y-0 flex-col gap-0 overflow-hidden border-0 bg-background p-0 shadow-2xl sm:top-1/2 sm:bottom-auto sm:max-h-[90vh] sm:w-full sm:max-w-[min(940px,calc(100vw-2rem))] sm:-translate-y-1/2 sm:rounded-3xl"
      >
        <DialogHeader className="relative shrink-0 overflow-hidden border-b bg-[radial-gradient(circle_at_top_right,hsl(var(--primary)/0.15),transparent_32%),linear-gradient(135deg,hsl(var(--muted)/0.72),hsl(var(--background))_62%)] px-4 py-4 pr-14 sm:px-6 sm:py-5">
          <div className="pointer-events-none absolute -right-12 -top-16 size-40 rounded-full bg-primary/10 blur-3xl" />
          <DialogClose
            render={
              <Button
                type="button"
                variant="ghost"
                size="icon-sm"
                className="absolute right-3 top-3 z-10 rounded-full bg-background/80 text-muted-foreground shadow-sm ring-1 ring-border/70 backdrop-blur hover:bg-background hover:text-foreground"
              />
            }
          >
            <X className="size-4" />
            <span className="sr-only">Close</span>
          </DialogClose>
          <div className="relative flex min-w-0 items-start gap-3 sm:gap-4">
            <div className="flex size-12 shrink-0 items-center justify-center rounded-2xl bg-primary/10 text-primary ring-1 ring-primary/15">
              <Bot className="size-5" />
            </div>
            <div className="min-w-0 flex-1">
              <div className="mb-2 inline-flex items-center gap-1.5 rounded-full border bg-background/75 px-2.5 py-1 text-[11px] font-medium text-muted-foreground shadow-sm">
                <Sparkles className="size-3 text-primary" />
                {t(($) => $.windy.hiring_card_badge)}
              </div>
              <DialogTitle className="break-words text-lg font-semibold tracking-tight sm:text-xl">{t(($) => $.windy.create_title, { name: draft.name })}</DialogTitle>
              <DialogDescription className="mt-2 max-w-2xl text-sm leading-6">
                {t(($) => $.windy.create_description)}
              </DialogDescription>
            </div>
          </div>
        </DialogHeader>
        <div className="min-h-0 flex-1 space-y-4 overflow-y-auto px-4 py-4 sm:space-y-5 sm:px-6 sm:py-5">
          <div className="rounded-2xl border bg-card p-4 shadow-sm">
            <div className="mb-3 flex flex-col gap-3 sm:flex-row sm:items-start sm:justify-between">
              <div className="min-w-0">
                <p className="break-words text-sm font-semibold">{draft.description || draft.name}</p>
                <p className="mt-0.5 text-xs text-muted-foreground">{t(($) => $.windy.generated_hint)}</p>
              </div>
              <span className="w-fit shrink-0 rounded-full border bg-muted/40 px-2.5 py-1 text-[11px] font-medium text-muted-foreground">
                {draft.visibility === "workspace" ? "Workspace" : "Private"}
              </span>
            </div>
            {draft.instructions && (
              <p className="max-h-40 overflow-y-auto whitespace-pre-wrap break-words rounded-xl border bg-muted/25 px-3 py-2 text-xs leading-5 text-muted-foreground">
                {draft.instructions}
              </p>
            )}
          </div>
          {!hasUsableRuntime && !runtimesLoading ? (
            <div className="rounded-2xl border border-dashed bg-muted/20 px-4 py-6 text-center text-sm text-muted-foreground">
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
