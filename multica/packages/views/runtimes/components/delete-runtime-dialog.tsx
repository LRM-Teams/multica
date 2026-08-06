"use client";

import { useEffect, useMemo, useRef, useState } from "react";
import { Check, Clock, Copy, Loader2, Terminal } from "lucide-react";
import { showErrorToast } from "@multica/ui/lib/error-toast";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { ApiError } from "@multica/core/api";
import type { Agent, AgentRuntime } from "@multica/core/types";
import { useDeleteRuntime } from "@multica/core/runtimes/mutations";
import { runtimeKeys } from "@multica/core/runtimes/queries";
import { agentListOptions } from "@multica/core/workspace/queries";
import {
  type AgentPresenceDetail,
  useWorkspacePresenceMap,
} from "@multica/core/agents";
import { useWorkspacePaths } from "@multica/core/paths";
import { copyText } from "@multica/ui/lib/clipboard";
import { CODE_LIGATURE_CLASS } from "@multica/ui/lib/code-style";
import { cn } from "@multica/ui/lib/utils";
import {
  AlertDialog,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogTitle,
} from "@multica/ui/components/ui/alert-dialog";
import { Button } from "@multica/ui/components/ui/button";
import { resolveActorIdentityPresentation } from "@multica/core/identity";
import { ActorAvatar } from "../../common/actor-avatar";
import { ActorIdentityRow } from "../../common/actor-identity-row";
import { AppLink } from "../../navigation/app-link";
import { presentAgentActivityBand, resolveAgentActivityBand } from "../../agents/resolve-agent-live-status";
import { useT } from "../../i18n";
import { isSelfHealingRuntime } from "../utils";
import { splitRuntimeName } from "./runtime-machines";

// The "device" shown in the stop-daemon step should be the actual machine
// identity, not the runtime's (often provider-branded, e.g. "Claude")
// display name. Prefer the resolved hostname suffix ("Claude (build-01)" →
// "build-01"), then the daemon-reported device_info's leading segment
// ("host.local · 2.1.121" → "host.local"), and only fall back to the raw
// runtime name when neither is available.
function resolveDeviceLabel(runtime: AgentRuntime): string {
  const { hostname } = splitRuntimeName(runtime.name);
  if (hostname) return hostname;
  const infoHost = runtime.device_info.split(" · ")[0]?.trim();
  if (infoHost) return infoHost;
  return runtime.name;
}

// DeleteRuntimeDialog is the single confirmation surface for runtime
// deletion across the list-page kebab and the detail-page Diagnostics
// card. The delete entry is ALWAYS visible (Frank, #666: hiding it made
// the feature look broken); this dialog is instead a guided, three-step
// flow so the user always learns what to do next rather than hitting a
// dead end:
//
//   1. Active agents still bound → block. List them with a direct link to
//      each so the user can go archive/delete them there. We never
//      silently cascade-archive agents from this dialog (#666, explicit
//      product directive) — the user does that deliberately, elsewhere.
//   2. No agents, but the runtime is a running local daemon (self-healing
//      — see isSelfHealingRuntime, which uses derived health so a stale
//      ONLINE status that the Health column already shows as Offline does
//      NOT trap the user here) → block. Show the device name and a
//      copyable `multica daemon stop` command; poll the runtime list
//      while this step is showing so the dialog advances the moment the
//      daemon actually goes offline, without the user needing to reopen it.
//   3. No agents, not self-healing → the final irreversible confirm,
//      which performs the actual DELETE.
//
// Step 1 is re-derived every render from the live agent-list query (not
// re-checked only on open), so archiving an agent in another tab while
// this dialog is open advances it automatically.
export interface DeleteRuntimeDialogProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  runtime: AgentRuntime;
  wsId: string;
  // Called after a successful delete. List page closes the dialog and
  // toasts; detail page additionally navigates back to /computers.
  onDeleted: () => void;
}

export function DeleteRuntimeDialog({
  open,
  onOpenChange,
  runtime,
  wsId,
  onDeleted,
}: DeleteRuntimeDialogProps) {
  const { t } = useT("runtimes");
  const qc = useQueryClient();
  // Pull cached workspace data — every consumer page already has this
  // mounted, so this dialog adds zero new fetches when opened.
  const { data: agents = [] } = useQuery(agentListOptions(wsId));
  const { byAgent: presenceMap } = useWorkspacePresenceMap(wsId);

  // Re-derived every render the dialog is open (not just on open) so an
  // agent archived elsewhere, or a fresh 409 from the strict DELETE below,
  // advances the flow without the user having to reopen the dialog.
  const activeAgents = useMemo(
    () => agents.filter((a) => a.runtime_id === runtime.id && !a.archived_at),
    [agents, runtime.id],
  );
  // Server-issued override — set when the strict DELETE refuses with
  // `runtime_has_active_agents` because the cached list was stale. Once
  // set, it wins over the cache until the dialog is reopened.
  const [serverActiveAgents, setServerActiveAgents] = useState<Agent[] | null>(
    null,
  );
  const planAgents = serverActiveAgents ?? activeAgents;

  const [submitting, setSubmitting] = useState(false);
  const [planChangedNotice, setPlanChangedNotice] = useState<string | null>(
    null,
  );

  // Reset transient state every time the dialog opens so a previous
  // attempt's notice/override doesn't leak into the next one. Adjusted
  // inline during render (prev-prop comparison) rather than in an effect,
  // so there's no extra render with stale state between open and reset.
  // `prevOpenRef` is a comparison sentinel only — it's never rendered — so
  // a ref avoids the wasted re-render a useState copy would cost.
  const prevOpenRef = useRef(open);
  if (open !== prevOpenRef.current) {
    prevOpenRef.current = open;
    if (open) {
      setServerActiveAgents(null);
      setSubmitting(false);
      setPlanChangedNotice(null);
    }
  }

  const hasActiveAgents = planAgents.length > 0;
  const selfHealing = !hasActiveAgents && isSelfHealingRuntime(runtime);

  // Step 2 (waiting for the local daemon to go offline) needs to notice a
  // status flip promptly, not whenever the runtime list's normal staleTime
  // happens to elapse. Poll only while this exact step is visible.
  useEffect(() => {
    if (!open || !selfHealing) return;
    const id = setInterval(() => {
      void qc.invalidateQueries({ queryKey: runtimeKeys.all(wsId) });
    }, 4_000);
    return () => clearInterval(id);
  }, [open, selfHealing, qc, wsId]);

  const deleteMutation = useDeleteRuntime(wsId);

  const handleConfirm = async () => {
    // Defensive re-check of the self-healing rule — the affordance is
    // reachable at this step, but a local daemon that came back online
    // while the dialog was open should still block the action.
    if (isSelfHealingRuntime(runtime)) {
      showErrorToast(t(($) => $.detail.delete_dialog.self_healing_blocked_toast));
      return;
    }

    setSubmitting(true);
    try {
      await deleteMutation.mutateAsync(runtime.id);
      onDeleted();
    } catch (err) {
      // The strict DELETE returns a structured 409 when active agents
      // were created between dialog-open and confirm. Surface the
      // server's authoritative list and let the render fall through to
      // the blocked-by-agents step — never auto-cascade.
      const conflict = parseActiveAgentsConflict(err);
      if (conflict) {
        setServerActiveAgents(conflict.activeAgents);
        setPlanChangedNotice(
          t(
            ($) =>
              $.detail.delete_dialog.blocked_by_agents
                .notice_runtime_has_active_agents,
          ),
        );
        return;
      }
      const message =
        err instanceof Error && err.message
          ? err.message
          : t(($) => $.detail.delete_dialog.delete_failed_toast);
      showErrorToast(message);
    } finally {
      setSubmitting(false);
    }
  };

  // Blocks closing mid-write so an accidental click on the underlying page
  // can't cancel a delete that's already in flight.
  const handleOpenChange = (next: boolean) => {
    if (submitting) return;
    onOpenChange(next);
  };

  const paths = useWorkspacePaths();

  return (
    <AlertDialog open={open} onOpenChange={handleOpenChange}>
      <AlertDialogContent
        className={
          hasActiveAgents
            ? "w-[calc(100vw-2rem)] !max-w-[560px] gap-0 overflow-hidden rounded-lg p-0"
            : "w-[calc(100vw-2rem)] !max-w-[440px] gap-0 overflow-hidden rounded-lg p-0"
        }
        onClick={(e) => e.stopPropagation()}
      >
        {hasActiveAgents ? (
          <AgentsBlockingBody
            runtime={runtime}
            agents={planAgents}
            presenceMap={presenceMap}
            agentHref={(id) => paths.agentDetail(id)}
            notice={planChangedNotice}
            onClose={() => handleOpenChange(false)}
          />
        ) : selfHealing ? (
          <StopDaemonBody
            runtime={runtime}
            onClose={() => handleOpenChange(false)}
          />
        ) : (
          <FinalConfirmBody
            runtime={runtime}
            submitting={submitting}
            onCancel={() => handleOpenChange(false)}
            onConfirm={handleConfirm}
          />
        )}
      </AlertDialogContent>
    </AlertDialog>
  );
}

// ---------------------------------------------------------------------------
// Step 1 — active agents still bound. No destructive action lives here; the
// only way forward is to go handle each agent, elsewhere.
// ---------------------------------------------------------------------------

function AgentsBlockingBody({
  runtime,
  agents,
  presenceMap,
  agentHref,
  notice,
  onClose,
}: {
  runtime: AgentRuntime;
  agents: Agent[];
  presenceMap: Map<string, AgentPresenceDetail>;
  agentHref: (agentId: string) => string;
  notice: string | null;
  onClose: () => void;
}) {
  const { t } = useT("runtimes");
  const count = agents.length;

  return (
    <>
      <div className="px-5 pb-4 pt-5">
        <AlertDialogTitle className="text-base font-semibold">
          {t(($) => $.detail.delete_dialog.blocked_by_agents.title, { count })}
        </AlertDialogTitle>
        <AlertDialogDescription className="mt-1 text-sm leading-5 text-muted-foreground">
          {t(($) => $.detail.delete_dialog.blocked_by_agents.description, {
            name: runtime.name,
          })}
        </AlertDialogDescription>

        {notice && (
          <div
            role="status"
            className="mt-2 rounded-md border bg-muted/40 px-3 py-2 text-xs text-foreground"
          >
            {notice}
          </div>
        )}

        <div className="mt-3 overflow-hidden rounded-md border divide-y">
          {agents.map((agent) => {
            const presentation = resolveActorIdentityPresentation(
              agent,
              agent.id,
            );
            const presence = presenceMap.get(agent.id);
            return (
              <AppLink
                key={agent.id}
                href={agentHref(agent.id)}
                className="flex items-center justify-between gap-3 px-3 py-2.5 text-xs hover:bg-muted/40"
              >
                <span className="inline-flex min-w-0 items-center gap-2">
                  <ActorAvatar
                    actorType="agent"
                    actorId={agent.id}
                    size={20}
                    enableHoverCard
                  />
                  <ActorIdentityRow
                    displayName={presentation.displayName}
                    handle={presentation.handle}
                    showHandle={presentation.showHandleLabel}
                    primaryClassName="truncate font-medium text-foreground"
                    secondaryClassName="truncate text-[11px] text-muted-foreground"
                  />
                </span>
                <span className="inline-flex shrink-0 items-center gap-1.5">
                  <PresenceCell presence={presence} />
                  <span className="text-primary">
                    {t(($) => $.detail.delete_dialog.blocked_by_agents.view_agent)}
                  </span>
                </span>
              </AppLink>
            );
          })}
        </div>
      </div>

      <div className="border-t bg-muted/25 px-5 py-3">
        <div className="flex justify-end">
          <Button type="button" variant="outline" onClick={onClose}>
            {t(($) => $.detail.delete_dialog.blocked_by_agents.close)}
          </Button>
        </div>
      </div>
    </>
  );
}

function PresenceCell({ presence }: { presence: AgentPresenceDetail | undefined }) {
  const { t } = useT("runtimes");
  if (!presence) {
    return (
      <span className="text-muted-foreground/60">
        {t(($) => $.detail.delete_dialog.blocked_by_agents.table.presence_unknown)}
      </span>
    );
  }
  // task #7 (2026-07-31): was availabilityConfig + workloadConfig — a single
  // combined cell (one dot, one label), no adjacent Status column elsewhere
  // in this dialog, so `showDisconnected: true` — Activity carries the
  // connectivity fact here since nothing else does.
  const band = resolveAgentActivityBand(presence);
  if (!band) return null;
  const view = presentAgentActivityBand(band, true);
  const isWorking = band === "working";
  const isQueued = presence.workload === "queued";
  return (
    <span className="inline-flex items-center gap-1.5">
      <span className={`size-1.5 shrink-0 rounded-full ${view.dotClass}`} />
      <span className="text-foreground">
        {isWorking && !isQueued && (
          <Loader2 className="mr-1 inline size-3 align-[-2px] animate-spin text-running" />
        )}
        {isQueued && <Clock className="mr-1 inline size-3 align-[-2px] text-muted-foreground" />}
        {view.label}
      </span>
    </span>
  );
}

// ---------------------------------------------------------------------------
// Step 2 — no agents left, but a local daemon is still online and will
// re-register itself seconds after a server-side delete. Give the user the
// exact command to stop it, and detect the moment it goes offline.
// ---------------------------------------------------------------------------

function StopDaemonBody({
  runtime,
  onClose,
}: {
  runtime: AgentRuntime;
  onClose: () => void;
}) {
  const { t } = useT("runtimes");
  const command = "multica daemon stop";
  const deviceLabel = resolveDeviceLabel(runtime);

  return (
    <>
      <div className="px-5 pb-4 pt-5">
        <AlertDialogTitle className="text-base font-semibold">
          {t(($) => $.detail.delete_dialog.blocked_by_online_daemon.title)}
        </AlertDialogTitle>
        <AlertDialogDescription className="mt-1 text-sm leading-5 text-muted-foreground">
          {t(($) => $.detail.delete_dialog.blocked_by_online_daemon.description, {
            name: runtime.name,
          })}
        </AlertDialogDescription>

        <div className="mt-3">
          <p className="mb-1.5 text-[11px] uppercase tracking-wide text-muted-foreground">
            {t(($) => $.detail.delete_dialog.blocked_by_online_daemon.device_label)}
          </p>
          <p className="text-sm font-medium">{deviceLabel}</p>
        </div>

        <div className="mt-3">
          <p className="mb-1.5 text-[11px] uppercase tracking-wide text-muted-foreground">
            {t(
              ($) =>
                $.detail.delete_dialog.blocked_by_online_daemon
                  .stop_command_label,
            )}
          </p>
          <CopyableCommand
            command={command}
            copyAria={t(
              ($) => $.detail.delete_dialog.blocked_by_online_daemon.copy_aria,
            )}
          />
        </div>

        <p role="status" className="mt-3 text-xs text-muted-foreground">
          {t(($) => $.detail.delete_dialog.blocked_by_online_daemon.waiting_hint)}
        </p>
      </div>

      <div className="border-t bg-muted/25 px-5 py-3">
        <div className="flex justify-end">
          <Button type="button" variant="outline" onClick={onClose}>
            {t(($) => $.detail.delete_dialog.blocked_by_online_daemon.close)}
          </Button>
        </div>
      </div>
    </>
  );
}

function CopyableCommand({
  command,
  copyAria,
}: {
  command: string;
  copyAria: string;
}) {
  const [copied, setCopied] = useState(false);

  useEffect(() => {
    if (!copied) return;
    const id = setTimeout(() => setCopied(false), 2000);
    return () => clearTimeout(id);
  }, [copied]);

  const handleCopy = () => {
    void copyText(command).then((ok) => {
      if (ok) setCopied(true);
    });
  };

  return (
    <div className="flex items-start gap-2 rounded-lg bg-muted px-3 py-2.5 font-mono text-sm">
      <Terminal className="mt-0.5 h-3.5 w-3.5 shrink-0 text-muted-foreground" aria-hidden />
      <code
        className={cn(
          "min-w-0 flex-1 break-all whitespace-pre-wrap",
          CODE_LIGATURE_CLASS,
        )}
      >
        {command}
      </code>
      <button
        type="button"
        onClick={handleCopy}
        aria-label={copyAria}
        className="shrink-0 rounded p-1 text-muted-foreground transition-colors hover:bg-accent hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
      >
        {copied ? (
          <Check className="h-3.5 w-3.5 text-success" aria-hidden />
        ) : (
          <Copy className="h-3.5 w-3.5" aria-hidden />
        )}
      </button>
    </div>
  );
}

// ---------------------------------------------------------------------------
// Step 3 — no agents, not self-healing: the final irreversible confirm.
// ---------------------------------------------------------------------------

function FinalConfirmBody({
  runtime,
  submitting,
  onCancel,
  onConfirm,
}: {
  runtime: AgentRuntime;
  submitting: boolean;
  onCancel: () => void;
  onConfirm: () => void;
}) {
  const { t } = useT("runtimes");
  return (
    <>
      <div className="px-5 pb-4 pt-5">
        <AlertDialogTitle className="text-base font-semibold">
          {t(($) => $.detail.delete_dialog.final.title)}
        </AlertDialogTitle>
        <AlertDialogDescription className="mt-1 text-sm leading-5 text-muted-foreground">
          {t(($) => $.detail.delete_dialog.final.description, {
            name: runtime.name,
          })}
        </AlertDialogDescription>
      </div>
      <div className="border-t bg-muted/25 px-5 py-3">
        <div className="flex flex-col-reverse gap-2 sm:flex-row sm:justify-end">
          <Button
            type="button"
            variant="outline"
            className="w-full sm:w-auto"
            onClick={onCancel}
            disabled={submitting}
          >
            {t(($) => $.detail.delete_dialog.final.cancel)}
          </Button>
          <Button
            type="button"
            variant="destructive"
            className="w-full sm:w-auto"
            onClick={onConfirm}
            disabled={submitting}
          >
            {submitting
              ? t(($) => $.detail.delete_dialog.final.submitting)
              : t(($) => $.detail.delete_dialog.final.confirm)}
          </Button>
        </div>
      </div>
    </>
  );
}

// ---------------------------------------------------------------------------
// Server response parsing
// ---------------------------------------------------------------------------

interface ActiveAgentsConflict {
  activeAgents: Agent[];
}

// parseActiveAgentsConflict pulls the structured 409 fields off an ApiError.
// Non-409s, non-matching codes, and missing bodies all collapse to `null` so
// callers can fall through to the generic error toast.
function parseActiveAgentsConflict(err: unknown): ActiveAgentsConflict | null {
  if (!(err instanceof ApiError)) return null;
  if (err.status !== 409) return null;
  const body = err.body;
  if (!body || typeof body !== "object") return null;
  const code = (body as Record<string, unknown>).code;
  if (code !== "runtime_has_active_agents") return null;
  const rawAgents = (body as Record<string, unknown>).active_agents;
  if (!Array.isArray(rawAgents)) {
    return { activeAgents: [] };
  }
  // We trust the server contract here — the response is the same
  // AgentResponse shape that the agent list endpoint returns. Light
  // runtime checks (id, name) catch genuinely malformed payloads without
  // re-typing every field.
  const activeAgents = rawAgents.filter(
    (a): a is Agent =>
      typeof a === "object" &&
      a !== null &&
      typeof (a as Record<string, unknown>).id === "string" &&
      typeof (a as Record<string, unknown>).name === "string",
  );
  return { activeAgents };
}
