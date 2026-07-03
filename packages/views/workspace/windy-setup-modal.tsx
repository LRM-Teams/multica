"use client";

import { useEffect, useMemo, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Bot, Loader2 } from "lucide-react";
import { toast } from "sonner";
import { api } from "@multica/core/api";
import { useAuthStore } from "@multica/core/auth";
import { useWorkspaceId } from "@multica/core/hooks";
import { dmKeys } from "@multica/core/dm";
import { runtimeListOptions } from "@multica/core/runtimes";
import { memberListOptions, workspaceKeys } from "@multica/core/workspace/queries";
import type { Agent } from "@multica/core/types";
import { Button } from "@multica/ui/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@multica/ui/components/ui/dialog";
import { cn } from "@multica/ui/lib/utils";
import { RuntimePicker, isRuntimeUsableForUser } from "../agents/components/runtime-picker";
import { ModelDropdown } from "../agents/components/model-dropdown";
import { ThinkingDropdown } from "../agents/components/thinking-dropdown";
import {
  JOE_AGENT_NAME,
  JOE_AVATAR_URL,
  JOE_DESCRIPTION,
  JOE_INSTRUCTIONS,
} from "../onboarding/templates";

const WINDY_SETUP_VERSION = "2026-07-03-windy-v1";

function setupStorageKey(workspaceId: string, userId: string): string {
  return `multica:windy-setup:${WINDY_SETUP_VERSION}:${workspaceId}:${userId}`;
}

export function WindySetupModal() {
  const wsId = useWorkspaceId();
  const user = useAuthStore((s) => s.user);
  const qc = useQueryClient();
  const { data: runtimes = [], isLoading: runtimesLoading, refetch: refetchRuntimes } = useQuery(runtimeListOptions(wsId));
  const { data: members = [] } = useQuery(memberListOptions(wsId));
  const [localReady, setLocalReady] = useState(false);
  const [configured, setConfigured] = useState(false);
  const [selectedRuntimeId, setSelectedRuntimeId] = useState("");
  const [model, setModel] = useState("");
  const [thinkingLevel, setThinkingLevel] = useState("");
  const [saving, setSaving] = useState(false);

  const storageKey = user ? setupStorageKey(wsId, user.id) : null;

  useEffect(() => {
    if (!storageKey) return;
    setConfigured(window.localStorage.getItem(storageKey) === "done");
    setLocalReady(true);
  }, [storageKey]);

  const selectedRuntime = useMemo(
    () => runtimes.find((r) => r.id === selectedRuntimeId) ?? null,
    [runtimes, selectedRuntimeId],
  );

  useEffect(() => {
    if (selectedRuntimeId || runtimesLoading) return;
    const firstUsable = runtimes.find((r) => isRuntimeUsableForUser(r, user?.id ?? null));
    if (firstUsable) setSelectedRuntimeId(firstUsable.id);
  }, [runtimes, runtimesLoading, selectedRuntimeId, user?.id]);

  if (!user || !localReady || configured) return null;

  const hasUsableRuntime = runtimes.some((r) => isRuntimeUsableForUser(r, user.id));

  const handleSubmit = async () => {
    if (!storageKey || !selectedRuntimeId || saving) return;
    setSaving(true);
    try {
      const ensured = await api.ensureJoe(selectedRuntimeId);
      const updated = await api.updateAgent(ensured.agent.id, {
        display_name: JOE_AGENT_NAME,
        description: JOE_DESCRIPTION,
        instructions: JOE_INSTRUCTIONS,
        avatar_url: JOE_AVATAR_URL,
        runtime_id: selectedRuntimeId,
        model: model.trim(),
        thinking_level: thinkingLevel,
        max_concurrent_tasks: 6,
      });
      qc.setQueryData<Agent[]>(workspaceKeys.agents(wsId), (current = []) => {
        const exists = current.some((a) => a.id === updated.id);
        return exists
          ? current.map((a) => (a.id === updated.id ? updated : a))
          : [...current, updated];
      });
      qc.invalidateQueries({ queryKey: workspaceKeys.agents(wsId) });
      if (ensured.dm_id) {
        await api.pinDM("dm_channel", ensured.dm_id);
      }
      qc.invalidateQueries({ queryKey: dmKeys.list(wsId) });
      window.localStorage.setItem(storageKey, "done");
      setConfigured(true);
      toast.success("Windy is ready and pinned in Direct Messages.");
    } catch (err) {
      toast.error(err instanceof Error ? err.message : "Failed to set up Windy");
    } finally {
      setSaving(false);
    }
  };

  return (
    <Dialog open onOpenChange={() => {}}>
      <DialogContent
        showCloseButton={false}
        className="max-w-xl gap-0 overflow-hidden p-0"
      >
        <DialogHeader className="border-b px-5 py-4">
          <div className="flex items-center gap-3">
            <div className="flex size-10 items-center justify-center rounded-xl bg-primary/10 text-primary">
              <Bot className="size-5" />
            </div>
            <div className="min-w-0">
              <DialogTitle>Set up Windy HR</DialogTitle>
              <DialogDescription className="mt-1">
                Required update for every member: choose where Windy runs, then we will refresh its runtime, model, and instructions.
              </DialogDescription>
            </div>
          </div>
        </DialogHeader>

        <div className="space-y-4 px-5 py-4">
          <div className="rounded-lg border bg-muted/30 px-3 py-2 text-xs text-muted-foreground">
            Windy recruits useful agents from your chat and keeps the generated hiring card in the current conversation. Its Direct Message is pinned after setup.
          </div>

          {!hasUsableRuntime && !runtimesLoading ? (
            <div className="rounded-lg border border-dashed px-4 py-5 text-center">
              <p className="text-sm font-medium">Connect or share a runtime first</p>
              <p className="mt-1 text-xs text-muted-foreground">
                Windy needs an available runtime before it can recruit agents or update its own instructions.
              </p>
              <Button
                type="button"
                variant="outline"
                size="sm"
                className="mt-3"
                onClick={() => refetchRuntimes()}
              >
                Refresh runtimes
              </Button>
            </div>
          ) : (
            <div className="grid gap-4 sm:grid-cols-2">
              <div className="sm:col-span-2">
                <RuntimePicker
                  runtimes={runtimes}
                  runtimesLoading={runtimesLoading}
                  members={members}
                  currentUserId={user.id}
                  selectedRuntimeId={selectedRuntimeId}
                  onSelect={setSelectedRuntimeId}
                />
              </div>
              <ModelDropdown
                runtimeId={selectedRuntimeId || null}
                runtimeOnline={selectedRuntime?.status === "online"}
                value={model}
                onChange={setModel}
                disabled={!selectedRuntimeId}
              />
              <ThinkingDropdown
                runtimeId={selectedRuntimeId || null}
                runtimeOnline={selectedRuntime?.status === "online"}
                model={model}
                value={thinkingLevel}
                onChange={setThinkingLevel}
                disabled={!selectedRuntimeId}
              />
            </div>
          )}
        </div>

        <DialogFooter className="items-center justify-between gap-3 sm:flex-row">
          <p className="text-xs text-muted-foreground">
            This prompt is one-time per member for the current Windy update.
          </p>
          <Button
            type="button"
            onClick={handleSubmit}
            disabled={!selectedRuntimeId || saving || !hasUsableRuntime}
            className={cn("min-w-32", saving && "cursor-wait")}
          >
            {saving ? <Loader2 className="size-4 animate-spin" /> : null}
            Update Windy
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
