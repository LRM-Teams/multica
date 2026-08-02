"use client";

import { Globe, Lock } from "lucide-react";
import { toast } from "sonner";
import { showErrorToast } from "@multica/ui/lib/error-toast";
import { useAuthStore } from "@multica/core/auth";
import { useWorkspaceId } from "@multica/core/hooks";
import { useQuery } from "@tanstack/react-query";
import { memberListOptions } from "@multica/core/workspace/queries";
import { useUpdateRuntime } from "@multica/core/runtimes/mutations";
import type { AgentRuntime } from "@multica/core/types";
import { cn } from "@multica/ui/lib/utils";
import { ProviderLogo } from "./provider-logo";
import { splitRuntimeName, type RuntimeMachine } from "./runtime-machines";
import { useT } from "../../i18n/use-t";

function providerLabel(runtime: AgentRuntime): string {
  switch (runtime.provider) {
    case "claude":
    case "claude-code":
      return "Claude Code";
    case "codex":
      return "Codex CLI";
    case "cursor":
      return "Cursor";
    case "opencode":
      return "OpenCode";
    case "openclaw":
      return "OpenClaw";
    case "hermes":
      return "Hermes";
    case "grok":
      return "Grok";
    case "pi":
      return "Pi";
    default: {
      const { base } = splitRuntimeName(runtime.name);
      return base || runtime.provider;
    }
  }
}

/**
 * Per-runtime visibility on the machine detail page (Iris 「共享设置」).
 * Owner is shown once on the machine header — rows are tool name + state only.
 * Lock reason is section-level once (Iris/Parker 2026-08-02), not per row.
 */
export function MachineSharingSection({ machine }: { machine: RuntimeMachine }) {
  const { t } = useT("runtimes");
  const wsId = useWorkspaceId();
  const user = useAuthStore((s) => s.user);
  const { data: members = [] } = useQuery(memberListOptions(wsId));
  const updateRuntime = useUpdateRuntime(wsId);

  const currentMember = user
    ? members.find((m) => m.user_id === user.id)
    : null;
  const isAdmin = currentMember
    ? currentMember.role === "owner" || currentMember.role === "admin"
    : false;

  if (machine.runtimes.length === 0) return null;

  // Stable order: provider name then id
  const rows = machine.runtimes.toSorted((a, b) => {
    const an = providerLabel(a).localeCompare(providerLabel(b));
    return an !== 0 ? an : a.id.localeCompare(b.id);
  });

  const canEditRuntime = (runtime: AgentRuntime) =>
    !!user && (runtime.owner_id === user.id || isAdmin);

  // Same fact for every locked row on a machine — say it once under the title.
  const showOwnerOnlyNote = rows.some((r) => !canEditRuntime(r));

  return (
    <section data-testid="machine-sharing-section">
      <h3 className="mb-2 text-[11px] font-medium uppercase tracking-wider text-muted-foreground">
        {t(($) => $.machine.sharing.section)}
      </h3>
      {showOwnerOnlyNote && (
        <p
          className="mb-2 px-1 text-xs text-muted-foreground"
          data-testid="machine-sharing-locked-reason"
        >
          {t(($) => $.machine.sharing.owner_only)}
        </p>
      )}
      <div className="overflow-hidden rounded-xl border bg-card">
        {rows.map((runtime, idx) => {
          const visibility =
            runtime.visibility === "public" ? "public" : "private";
          const canEdit = canEditRuntime(runtime);
          const next = visibility === "public" ? "private" : "public";

          const flip = () => {
            if (!canEdit) return;
            updateRuntime.mutate(
              { runtimeId: runtime.id, patch: { visibility: next } },
              {
                onSuccess: () =>
                  toast.success(
                    t(($) => $.detail.visibility_toast_updated, {
                      visibility: t(
                        ($) => $.detail.visibility_label[next],
                      ),
                    }),
                  ),
                onError: (err) =>
                  showErrorToast(
                    err instanceof Error && err.message
                      ? err.message
                      : t(($) => $.detail.visibility_toast_failed),
                  ),
              },
            );
          };

          return (
            <div
              key={runtime.id}
              className={cn(
                "flex flex-wrap items-center gap-2 px-4 py-3",
                idx < rows.length - 1 && "border-b",
              )}
              data-testid={`machine-sharing-row-${runtime.id}`}
            >
              <ProviderLogo
                provider={runtime.provider}
                className="h-3.5 w-3.5 shrink-0"
              />
              <span className="min-w-0 flex-1 text-sm">
                <span className="font-medium">{providerLabel(runtime)}</span>
                <span className="text-muted-foreground"> · </span>
                <span className="text-muted-foreground">
                  {t(($) => $.detail.visibility_label[visibility])}
                </span>
              </span>
              <button
                type="button"
                onClick={flip}
                disabled={!canEdit || updateRuntime.isPending}
                className={cn(
                  "inline-flex shrink-0 items-center gap-1.5 rounded-md border px-2 py-1 text-xs font-medium transition-colors",
                  canEdit
                    ? "hover:bg-accent"
                    : "cursor-not-allowed opacity-60",
                )}
                data-testid={`machine-sharing-toggle-${runtime.id}`}
              >
                {visibility === "public" ? (
                  <Lock className="h-3 w-3" />
                ) : (
                  <Globe className="h-3 w-3" />
                )}
                {canEdit
                  ? t(($) => $.machine.sharing.switch_to[next])
                  : t(($) => $.machine.sharing.switch_locked)}
              </button>
            </div>
          );
        })}
      </div>
    </section>
  );
}
