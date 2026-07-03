"use client";

import * as React from "react";
import { Bot, Loader2 } from "lucide-react";
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
import { listParam, parseJoeCreateAgentURL } from "./joe-create-agent-link-utils";

export function JoeCreateAgentLink({
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

  const handleClick = async () => {
    const url = parseJoeCreateAgentURL(href);
    if (!url || creatingDraft) return;
    const name = url.searchParams.get("name")?.trim() || "New Agent";
    setCreatingDraft(true);
    try {
      const createdDraft = await api.createAgentDraft({
        name,
        description: url.searchParams.get("description")?.trim() || "",
        instructions: url.searchParams.get("instructions")?.trim() || "",
        avatar_url: url.searchParams.get("avatar_url") || null,
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
        disabled={creatingDraft}
        onClick={handleClick}
        className={cn("not-prose my-1 inline-flex max-w-full gap-2", className)}
      >
        {creatingDraft ? <Loader2 className="size-3.5 animate-spin" /> : <Bot className="size-3.5" />}
        <span className="truncate">{children}</span>
      </Button>
      {draft && <InlineCreateAgentDialog draft={draft} onClose={() => setDraft(null)} />}
    </>
  );
}

function InlineCreateAgentDialog({
  draft,
  onClose,
}: {
  draft: AgentCreationDraft;
  onClose: () => void;
}) {
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
      toast.success(`${draft.name} created`);
      onClose();
    } catch (err) {
      toast.error(err instanceof Error ? err.message : "Failed to create agent");
    } finally {
      setCreating(false);
    }
  };

  return (
    <Dialog open onOpenChange={handleOpenChange}>
      <DialogContent className="max-w-lg gap-0 overflow-hidden p-0">
        <DialogHeader className="border-b px-5 py-4">
          <DialogTitle>Create Agent @{draft.name}</DialogTitle>
          <DialogDescription>
            Review Windy's hiring card, pick a runtime/model, then start the agent here.
          </DialogDescription>
        </DialogHeader>
        <div className="space-y-4 px-5 py-4">
          <div className="rounded-lg border bg-muted/30 px-3 py-2">
            <p className="text-sm font-medium">{draft.description || draft.name}</p>
            {draft.instructions && (
              <p className="mt-1 line-clamp-4 text-xs text-muted-foreground">
                {draft.instructions}
              </p>
            )}
          </div>
          {!hasUsableRuntime && !runtimesLoading ? (
            <div className="rounded-lg border border-dashed px-4 py-5 text-center text-sm text-muted-foreground">
              Connect or share a runtime before creating this agent.
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
        <DialogFooter>
          <Button type="button" variant="outline" onClick={onClose} disabled={creating}>
            Cancel
          </Button>
          <Button type="button" onClick={handleCreate} disabled={!selectedRuntime || creating || !hasUsableRuntime}>
            {creating ? <Loader2 className="size-4 animate-spin" /> : null}
            Create Agent
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
