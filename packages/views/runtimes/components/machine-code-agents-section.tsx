"use client";

import { useState, type ReactNode } from "react";
import { ExternalLink, Globe, Lock, SlidersHorizontal } from "lucide-react";
import { toast } from "sonner";
import { showErrorToast } from "@multica/ui/lib/error-toast";
import { useAuthStore } from "@multica/core/auth";
import { useWorkspaceId } from "@multica/core/hooks";
import { useQuery } from "@tanstack/react-query";
import { memberListOptions } from "@multica/core/workspace/queries";
import { useUpdateRuntime } from "@multica/core/runtimes/mutations";
import { Button } from "@multica/ui/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
} from "@multica/ui/components/ui/dialog";
import { cn } from "@multica/ui/lib/utils";
import { useT } from "../../i18n/use-t";
import type { RuntimeMachine } from "./runtime-machines";
import { partitionMachineCodeAgents } from "./machine-code-agents";
import { ProviderLogo } from "./provider-logo";
import { RuntimeEnvEditor } from "./runtime-env-editor";

function SectionTitle({ children }: { children: ReactNode }) {
  return (
    <h2 className="text-[11px] font-semibold uppercase tracking-wide text-muted-foreground">
      {children}
    </h2>
  );
}

/**
 * LRM-922 / LRM-863 / LRM-960 / LRM-1071 / LRM-1108 — Code agents inventory:
 * section title + provider grid. Installed status is a green dot + muted
 * version (A2); visibility toggle on the same footer row. A2.1 drops the
 * section-header installed/supported count.
 */
export function MachineCodeAgentsSection({
  machine,
}: {
  machine: RuntimeMachine;
}) {
  const { t } = useT("runtimes");
  const wsId = useWorkspaceId();
  const user = useAuthStore((s) => s.user);
  const { data: members = [] } = useQuery(memberListOptions(wsId));
  const updateRuntime = useUpdateRuntime(wsId);
  const [envTarget, setEnvTarget] = useState<{ id: string; label: string } | null>(null);

  const currentMember = user
    ? members.find((m) => m.user_id === user.id)
    : null;
  const isAdmin = currentMember
    ? currentMember.role === "owner" || currentMember.role === "admin"
    : false;

  const { installed, notInstalled } = partitionMachineCodeAgents(
    machine.runtimes,
  );
  const rows = [...installed, ...notInstalled];
  const installedCount = installed.length;

  const canEditRuntime = (ownerId: string | null | undefined) =>
    !!user && (!!ownerId && (ownerId === user.id || isAdmin));

  return (
    <section data-testid="machine-runtimes-section">
      <div className="mb-2 px-1">
        <SectionTitle>{t(($) => $.machine.code_agents_section)}</SectionTitle>
      </div>
      <div className="overflow-hidden rounded-xl border bg-card">
        {rows.length === 0 ? (
          <p className="px-4 py-3 text-sm text-muted-foreground">
            {t(($) => $.machine.code_agents_installed_empty)}
          </p>
        ) : (
          <div className="grid grid-cols-2 divide-x divide-y md:grid-cols-3">
            {rows.map((row, idx) => {
              const isInstalled = idx < installedCount;
              const runtime = row.runtimeId
                ? machine.runtimes.find((r) => r.id === row.runtimeId)
                : undefined;
              const visibility = row.visibility;
              const canEdit = canEditRuntime(runtime?.owner_id);
              const next =
                visibility === "public" ? "private" : ("public" as const);

              const flip = () => {
                if (!runtime || !canEdit || !visibility) return;
                updateRuntime.mutate(
                  { runtimeId: runtime.id, patch: { visibility: next } },
                  {
                    onSuccess: () =>
                      toast.success(
                        t(($) => $.detail.visibility_toast_updated, {
                          visibility: t(($) => $.detail.visibility_label[next]),
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

              const versionLabel = row.version
                ? t(($) => $.machine.code_agents_status_installed_ver, {
                    version: row.version,
                  })
                : null;

              return (
                <article
                  key={row.id}
                  className="flex min-h-[96px] flex-col p-3.5 md:min-h-[110px]"
                  data-testid={
                    isInstalled
                      ? `machine-runtime-card-${row.id}`
                      : `machine-runtime-card-missing-${row.id}`
                  }
                  aria-label={
                    isInstalled
                      ? [
                          row.label,
                          t(($) => $.machine.code_agents_status_installed),
                          versionLabel,
                          visibility
                            ? t(($) => $.detail.visibility_label[visibility])
                            : null,
                        ]
                          .filter(Boolean)
                          .join(", ")
                      : [
                          row.label,
                          t(($) => $.machine.code_agents_not_installed),
                        ].join(", ")
                  }
                >
                  <div className="flex items-center gap-2 text-sm font-semibold">
                    <span
                      className={cn(
                        "shrink-0",
                        !isInstalled && "opacity-40 grayscale",
                      )}
                    >
                      <ProviderLogo
                        provider={row.id}
                        className="h-5 w-5"
                      />
                    </span>
                    <span className="min-w-0 truncate">{row.label}</span>
                  </div>
                  {isInstalled ? (
                    <div className="mt-auto flex flex-col items-start gap-2 pt-3 md:flex-row md:items-center md:justify-between md:gap-2">
                      <span className="inline-flex min-w-0 items-center gap-1.5 text-[11px] text-muted-foreground">
                        <span
                          className="inline-block h-1.5 w-1.5 shrink-0 rounded-full bg-online"
                          aria-hidden
                        />
                        {versionLabel ? (
                          <span className="tabular-nums">{versionLabel}</span>
                        ) : null}
                      </span>
                      {runtime ? (
                        <div className="flex items-center gap-1.5">
                          {canEdit ? (
                            <Button
                              type="button"
                              variant="outline"
                              size="xs"
                              className="h-6 shrink-0 gap-1 px-2 text-[11px]"
                              onClick={() => setEnvTarget({ id: runtime.id, label: row.label })}
                              data-testid={`machine-env-${runtime.id}`}
                            >
                              <SlidersHorizontal className="h-3 w-3" />
                              {t(($) => $.machine.code_agents_env)}
                            </Button>
                          ) : null}
                          {visibility ? (
                            <Button
                              type="button"
                              variant="outline"
                              size="xs"
                              className="h-6 shrink-0 gap-1 px-2 text-[11px]"
                              onClick={flip}
                              disabled={!canEdit || updateRuntime.isPending}
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
                            </Button>
                          ) : null}
                        </div>
                      ) : null}
                    </div>
                  ) : (
                    <div className="mt-auto pt-3">
                      {row.docsUrl ? (
                        <a
                          href={row.docsUrl}
                          target="_blank"
                          rel="noopener noreferrer"
                          className="inline-flex items-center gap-1 text-[11px] font-semibold text-brand underline-offset-2 hover:underline"
                        >
                          {t(($) => $.machine.code_agents_docs)}
                          <ExternalLink className="h-3 w-3" aria-hidden />
                        </a>
                      ) : null}
                    </div>
                  )}
                </article>
              );
            })}
          </div>
        )}
      </div>

      <Dialog
        open={envTarget !== null}
        onOpenChange={(open) => {
          if (!open) setEnvTarget(null);
        }}
      >
        <DialogContent className="max-w-md">
          <DialogHeader>
            <DialogTitle className="text-sm">
              {t(($) => $.machine.code_agents_env_dialog_title)}
              {envTarget ? ` · ${envTarget.label}` : ""}
            </DialogTitle>
          </DialogHeader>
          {envTarget ? (
            <RuntimeEnvEditor
              runtimeId={envTarget.id}
              onCancel={() => setEnvTarget(null)}
            />
          ) : null}
        </DialogContent>
      </Dialog>
    </section>
  );
}
