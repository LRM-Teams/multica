"use client";

import { useMemo, useReducer } from "react";
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
import { useT } from "../i18n";
import { RuntimePicker, isRuntimeUsableForUser } from "../agents/components/runtime-picker";
import { ModelDropdown } from "../agents/components/model-dropdown";
import { ThinkingDropdown } from "../agents/components/thinking-dropdown";
import {
  WINDY_AGENT_NAME,
  WINDY_AVATAR_URL,
  WINDY_DESCRIPTION,
  WINDY_INSTRUCTIONS,
} from "../onboarding/templates";

const WINDY_SETUP_VERSION = "2026-07-03-windy-v1";

type WindySetupState = {
  configuredKey: string | null;
  selectedRuntimeId: string;
  model: string;
  thinkingLevel: string;
  saving: boolean;
};

function setupStorageKey(workspaceId: string, userId: string): string {
  return `multica:windy-setup:${WINDY_SETUP_VERSION}:${workspaceId}:${userId}`;
}

function isStorageKeyDone(storageKey: string | null): boolean {
  return !!storageKey && typeof window !== "undefined" && window.localStorage.getItem(storageKey) === "done";
}

export function WindySetupModal() {
  const { t } = useT("workspace");
  const wsId = useWorkspaceId();
  const user = useAuthStore((s) => s.user);
  const qc = useQueryClient();
  const { data: runtimes = [], isLoading: runtimesLoading, refetch: refetchRuntimes } = useQuery(runtimeListOptions(wsId));
  const { data: members = [] } = useQuery(memberListOptions(wsId));
  const storageKey = user ? setupStorageKey(wsId, user.id) : null;
  const [{ configuredKey, selectedRuntimeId, model, thinkingLevel, saving }, updateState] = useReducer(
    (state: WindySetupState, patch: Partial<WindySetupState>) => ({ ...state, ...patch }),
    {
      configuredKey: isStorageKeyDone(storageKey) ? storageKey : null,
      selectedRuntimeId: "",
      model: "",
      thinkingLevel: "",
      saving: false,
    },
  );

  const firstUsableRuntimeId = useMemo(
    () => runtimes.find((r) => isRuntimeUsableForUser(r, user?.id ?? null))?.id ?? "",
    [runtimes, user?.id],
  );
  const effectiveRuntimeId = selectedRuntimeId || firstUsableRuntimeId;
  const selectedRuntime = useMemo(
    () => runtimes.find((r) => r.id === effectiveRuntimeId) ?? null,
    [effectiveRuntimeId, runtimes],
  );

  if (!user || !storageKey || configuredKey === storageKey || isStorageKeyDone(storageKey)) return null;

  const hasUsableRuntime = runtimes.some((r) => isRuntimeUsableForUser(r, user.id));

  const handleSubmit = async () => {
    if (!storageKey || !effectiveRuntimeId || saving) return;
    updateState({ saving: true });
    try {
      const ensured = await api.ensureWindy(effectiveRuntimeId);
      const updated = await api.updateAgent(ensured.agent.id, {
        display_name: WINDY_AGENT_NAME,
        description: WINDY_DESCRIPTION,
        instructions: WINDY_INSTRUCTIONS,
        avatar_url: WINDY_AVATAR_URL,
        runtime_id: effectiveRuntimeId,
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
      updateState({ configuredKey: storageKey });
      toast.success("Windy is ready and pinned in Direct Messages.");
    } catch (err) {
      toast.error(err instanceof Error ? err.message : "Failed to set up Windy");
    } finally {
      updateState({ saving: false });
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
              <DialogTitle>{t(($) => $.windy_setup.title)}</DialogTitle>
              <DialogDescription className="mt-1">
                {t(($) => $.windy_setup.description)}
              </DialogDescription>
            </div>
          </div>
        </DialogHeader>

        <div className="space-y-4 px-5 py-4">
          <div className="rounded-lg border bg-muted/30 px-3 py-2 text-xs text-muted-foreground">
            {t(($) => $.windy_setup.intro)}
          </div>

          {!hasUsableRuntime && !runtimesLoading ? (
            <div className="rounded-lg border border-dashed px-4 py-5 text-center">
              <p className="text-sm font-medium">{t(($) => $.windy_setup.runtime_required_title)}</p>
              <p className="mt-1 text-xs text-muted-foreground">
                {t(($) => $.windy_setup.runtime_required_description)}
              </p>
              <Button
                type="button"
                variant="outline"
                size="sm"
                className="mt-3"
                onClick={() => refetchRuntimes()}
              >
                {t(($) => $.windy_setup.refresh_runtimes)}
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
                  selectedRuntimeId={effectiveRuntimeId}
                  onSelect={(runtimeId) => updateState({ selectedRuntimeId: runtimeId })}
                />
              </div>
              <ModelDropdown
                runtimeId={effectiveRuntimeId || null}
                runtimeOnline={selectedRuntime?.status === "online"}
                value={model}
                onChange={(nextModel) => updateState({ model: nextModel })}
                disabled={!effectiveRuntimeId}
              />
              <ThinkingDropdown
                runtimeId={effectiveRuntimeId || null}
                runtimeOnline={selectedRuntime?.status === "online"}
                model={model}
                value={thinkingLevel}
                onChange={(nextLevel) => updateState({ thinkingLevel: nextLevel })}
                disabled={!effectiveRuntimeId}
              />
            </div>
          )}
        </div>

        <DialogFooter className="items-center justify-between gap-3 sm:flex-row">
          <p className="text-xs text-muted-foreground">
            {t(($) => $.windy_setup.one_time_note)}
          </p>
          <Button
            type="button"
            onClick={handleSubmit}
            disabled={!effectiveRuntimeId || saving || !hasUsableRuntime}
            className={cn("min-w-32", saving && "cursor-wait")}
          >
            {saving ? <Loader2 className="size-4 animate-spin" /> : null}
            {t(($) => $.windy_setup.update)}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
