"use client";

import { useMemo, useReducer } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Bot, CheckCircle2, Loader2, Sparkles } from "lucide-react";
import { toast } from "sonner";
import { api } from "@multica/core/api";
import { useAuthStore } from "@multica/core/auth";
import { useWorkspaceId } from "@multica/core/hooks";
import { dmKeys } from "@multica/core/dm";
import { runtimeListOptions } from "@multica/core/runtimes";
import { agentListOptions, memberListOptions, workspaceKeys } from "@multica/core/workspace/queries";
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
import { accountHasConfiguredWindy } from "./windy-setup-detection";

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
  const { data: agents = [] } = useQuery(agentListOptions(wsId));
  const storageKey = user ? setupStorageKey(wsId, user.id) : null;
  // An account that already has a Wendy agent with a runtime configured has
  // been through setup — a WINDY_SETUP_VERSION bump must not re-block it (#219).
  const hasConfiguredWendy = useMemo(() => accountHasConfiguredWindy(agents), [agents]);
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

  if (
    !user ||
    !storageKey ||
    configuredKey === storageKey ||
    isStorageKeyDone(storageKey) ||
    hasConfiguredWendy
  ) {
    return null;
  }

  const hasUsableRuntime = runtimes.some((r) => isRuntimeUsableForUser(r, user.id));

  // Dismiss without setting up — the setup is a one-time nudge, never a hard
  // block. Mark the version done so declining (or a failed discovery) doesn't
  // re-trap the user out of channels/issues.
  const handleDismiss = () => {
    if (storageKey && typeof window !== "undefined") window.localStorage.setItem(storageKey, "done");
    updateState({ configuredKey: storageKey });
  };

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
      toast.success("Wendy is ready and pinned in Direct Messages.");
    } catch (err) {
      toast.error(err instanceof Error ? err.message : "Failed to set up Wendy");
    } finally {
      updateState({ saving: false });
    }
  };

  return (
    <Dialog
      open
      onOpenChange={(open) => {
        if (!open) handleDismiss();
      }}
    >
      <DialogContent
        className="max-h-[90vh] max-w-[min(920px,calc(100vw-2rem))] gap-0 overflow-hidden border-0 bg-background p-0 shadow-2xl sm:rounded-3xl"
      >
        <DialogHeader className="relative overflow-hidden border-b bg-[radial-gradient(circle_at_top_left,hsl(var(--primary)/0.16),transparent_34%),linear-gradient(135deg,hsl(var(--muted)/0.78),hsl(var(--background))_58%)] px-6 py-5">
          <div className="pointer-events-none absolute right-6 top-5 h-24 w-24 rounded-full border border-primary/10 bg-primary/5 blur-xl" />
          <div className="relative flex items-start gap-4">
            <div className="flex size-12 items-center justify-center rounded-2xl bg-primary/10 text-primary ring-1 ring-primary/15">
              <Bot className="size-5" />
            </div>
            <div className="min-w-0">
              <div className="mb-2 inline-flex items-center gap-1.5 rounded-full border bg-background/75 px-2.5 py-1 text-[11px] font-medium text-muted-foreground shadow-sm">
                <Sparkles className="size-3 text-primary" />
                {t(($) => $.windy_setup.badge_label)}
              </div>
              <DialogTitle className="text-xl font-semibold tracking-tight">{t(($) => $.windy_setup.title)}</DialogTitle>
              <DialogDescription className="mt-2 max-w-2xl text-sm leading-6">
                {t(($) => $.windy_setup.description)}
              </DialogDescription>
            </div>
          </div>
        </DialogHeader>

        <div className="max-h-[calc(90vh-11rem)] space-y-5 overflow-y-auto px-6 py-5">
          <div className="grid gap-3 sm:grid-cols-3">
            <div className="rounded-2xl border bg-card p-3 shadow-sm sm:col-span-2">
              <p className="text-sm font-medium">{t(($) => $.windy_setup.intro_title)}</p>
              <p className="mt-1 text-xs leading-5 text-muted-foreground">
                {t(($) => $.windy_setup.intro)}
              </p>
            </div>
            <div className="rounded-2xl border bg-muted/35 p-3 text-xs leading-5 text-muted-foreground">
              <div className="mb-1 flex items-center gap-1.5 font-medium text-foreground">
                <CheckCircle2 className="size-3.5 text-success" />
                {t(($) => $.windy_setup.one_time_label)}
              </div>
              {t(($) => $.windy_setup.one_time_note)}
            </div>
          </div>

          {!hasUsableRuntime && !runtimesLoading ? (
            <div className="rounded-2xl border border-dashed bg-muted/20 px-4 py-6 text-center">
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

        <DialogFooter className="border-t bg-muted/25 px-6 py-4 sm:items-center sm:justify-between sm:flex-row">
          <p className="text-xs text-muted-foreground">
            {t(($) => $.windy_setup.runtime_move_note)}
          </p>
          <div className="flex items-center gap-2">
            <Button type="button" variant="ghost" onClick={handleDismiss} disabled={saving}>
              {t(($) => $.windy_setup.later)}
            </Button>
            <Button
              type="button"
              onClick={handleSubmit}
              disabled={!effectiveRuntimeId || saving || !hasUsableRuntime}
              className={cn("min-w-32", saving && "cursor-wait")}
            >
              {saving ? <Loader2 className="size-4 animate-spin" /> : null}
              {t(($) => $.windy_setup.update)}
            </Button>
          </div>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
