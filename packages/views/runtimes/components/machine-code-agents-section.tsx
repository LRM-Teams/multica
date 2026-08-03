"use client";

import type { ReactNode } from "react";
import { ExternalLink, Globe, Lock } from "lucide-react";
import { toast } from "sonner";
import { showErrorToast } from "@multica/ui/lib/error-toast";
import { useAuthStore } from "@multica/core/auth";
import { useWorkspaceId } from "@multica/core/hooks";
import { useQuery } from "@tanstack/react-query";
import { memberListOptions } from "@multica/core/workspace/queries";
import { useUpdateRuntime } from "@multica/core/runtimes/mutations";
import { Button } from "@multica/ui/components/ui/button";
import { cn } from "@multica/ui/lib/utils";
import { useT } from "../../i18n/use-t";
import type { RuntimeMachine } from "./runtime-machines";
import { partitionMachineCodeAgents } from "./machine-code-agents";
import { ProviderLogo } from "./provider-logo";

function SectionTitle({ children }: { children: ReactNode }) {
  return (
    <h2 className="text-[11px] font-semibold uppercase tracking-wide text-muted-foreground">
      {children}
    </h2>
  );
}

/**
 * LRM-922 / LRM-863 / LRM-960 / LRM-1071 — Code agents inventory:
 * section title + count + provider grid. Visibility toggle on installed
 * cards (Make public / Make private) replaces the separate Sharing list.
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
  const supportedMore = notInstalled.length;

  const canEditRuntime = (ownerId: string | null | undefined) =>
    !!user && (!!ownerId && (ownerId === user.id || isAdmin));

  return (
    <section data-testid="machine-runtimes-section">
      <div className="mb-2 flex items-baseline justify-between gap-3 px-1">
        <SectionTitle>{t(($) => $.machine.code_agents_section)}</SectionTitle>
        {rows.length > 0 ? (
          <span className="shrink-0 text-[11px] text-muted-foreground">
            {t(($) => $.machine.code_agents_help, {
              installed: installedCount,
              supported: installedCount + supportedMore,
            })}
          </span>
        ) : null}
      </div>
      <div className="overflow-hidden rounded-xl border bg-card">
        {rows.length === 0 ? (
          <p className="px-4 py-3 text-sm text-muted-foreground">
            {t(($) => $.machine.code_agents_installed_empty)}
          </p>
        ) : (
          <div className="grid grid-cols-2 divide-x divide-y md:grid-cols-4">
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

              return (
                <article
                  key={row.id}
                  className="flex min-h-[96px] flex-col p-3.5 md:min-h-[110px]"
                  data-testid={
                    isInstalled
                      ? `machine-runtime-card-${row.id}`
                      : `machine-runtime-card-missing-${row.id}`
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
                    <>
                      <p className="mt-2 text-[11px] text-emerald-700 dark:text-emerald-400">
                        <span className="inline-flex items-center gap-1.5">
                          <span
                            className="inline-block h-1.5 w-1.5 rounded-full bg-current"
                            aria-hidden
                          />
                          {row.version
                            ? t(($) => $.machine.code_agents_status_installed_ver, {
                                version: row.version,
                              })
                            : t(($) => $.machine.code_agents_status_installed)}
                          {visibility ? (
                            <>
                              <span aria-hidden>·</span>
                              <span className="text-muted-foreground">
                                {t(($) => $.detail.visibility_label[visibility])}
                              </span>
                            </>
                          ) : null}
                        </span>
                      </p>
                      {runtime && visibility ? (
                        <div className="mt-auto pt-3">
                          <Button
                            type="button"
                            variant="outline"
                            size="xs"
                            className="h-6 gap-1 px-2 text-[11px]"
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
                        </div>
                      ) : null}
                    </>
                  ) : (
                    <div className="mt-2">
                      <p className="text-[11px] text-muted-foreground">
                        <span className="inline-flex items-center gap-1.5">
                          <span
                            className="inline-block h-1.5 w-1.5 rounded-full bg-muted-foreground/55"
                            aria-hidden
                          />
                          {t(($) => $.machine.code_agents_status_supported)}
                        </span>
                      </p>
                      {row.docsUrl ? (
                        <a
                          href={row.docsUrl}
                          target="_blank"
                          rel="noopener noreferrer"
                          className="mt-1 inline-flex items-center gap-1 text-[11px] font-medium text-brand underline-offset-2 hover:underline"
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
    </section>
  );
}
