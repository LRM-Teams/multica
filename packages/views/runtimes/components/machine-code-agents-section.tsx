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
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from "@multica/ui/components/ui/tooltip";
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
 * Card action: icon-only button with a tooltip. The visible label is sr-only
 * so the accessible name (and the card's text content) still carries it —
 * a 3-column grid has no room for two full-text buttons per card, which is
 * what made the old footer wrap onto two lines.
 */
function CardAction({
  label,
  icon,
  onClick,
  disabled,
  testId,
}: {
  label: string;
  icon: ReactNode;
  onClick?: () => void;
  disabled?: boolean;
  testId?: string;
}) {
  return (
    <Tooltip>
      <TooltipTrigger
        render={
          <Button
            type="button"
            variant="ghost"
            size="icon-xs"
            onClick={onClick}
            disabled={disabled}
            className="text-muted-foreground hover:text-foreground"
            data-testid={testId}
          />
        }
      >
        {icon}
        <span className="sr-only">{label}</span>
      </TooltipTrigger>
      <TooltipContent>{label}</TooltipContent>
    </Tooltip>
  );
}

/**
 * LRM-922 / LRM-863 / LRM-960 / LRM-1071 / LRM-1108 — Code agents inventory:
 * section title + provider grid. Installed status is a green dot + muted
 * version (A2); the per-card actions sit on the same line as the name as
 * icon buttons so cards stay one compact row each.
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
  const [envTarget, setEnvTarget] = useState<{
    id: string;
    label: string;
    provider: string;
  } | null>(null);

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
          <div className="grid grid-cols-1 divide-x divide-y sm:grid-cols-2 lg:grid-cols-3">
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
                  className="flex items-center gap-3 px-3.5 py-3 transition-colors hover:bg-muted/40"
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
                  <span
                    className={cn(
                      "flex size-8 shrink-0 items-center justify-center rounded-lg border bg-background",
                      !isInstalled && "opacity-40 grayscale",
                    )}
                  >
                    <ProviderLogo provider={row.id} className="size-4.5" />
                  </span>

                  <div className="min-w-0 flex-1">
                    <div
                      className={cn(
                        "truncate text-sm font-medium",
                        !isInstalled && "text-muted-foreground",
                      )}
                    >
                      {row.label}
                    </div>
                    <div className="mt-0.5 flex min-w-0 items-center gap-1.5 text-[11px] text-muted-foreground">
                      <span
                        className={cn(
                          "inline-block size-1.5 shrink-0 rounded-full",
                          isInstalled
                            ? "bg-online"
                            : "border border-muted-foreground/40",
                        )}
                        aria-hidden
                      />
                      <span className="truncate tabular-nums">
                        {isInstalled
                          ? (versionLabel ??
                            t(($) => $.machine.code_agents_status_installed))
                          : t(($) => $.machine.code_agents_not_installed)}
                      </span>
                    </div>
                  </div>

                  <div className="flex shrink-0 items-center gap-0.5">
                    {isInstalled && runtime ? (
                      <>
                        {canEdit ? (
                          <CardAction
                            label={t(($) => $.machine.code_agents_env)}
                            icon={<SlidersHorizontal />}
                            onClick={() =>
                              setEnvTarget({
                                id: runtime.id,
                                label: row.label,
                                provider: row.id,
                              })
                            }
                            testId={`machine-env-${runtime.id}`}
                          />
                        ) : null}
                        {visibility ? (
                          <CardAction
                            label={
                              canEdit
                                ? t(($) => $.machine.sharing.switch_to[next])
                                : t(($) => $.machine.sharing.switch_locked)
                            }
                            icon={
                              visibility === "public" ? <Lock /> : <Globe />
                            }
                            onClick={flip}
                            disabled={!canEdit || updateRuntime.isPending}
                            testId={`machine-sharing-toggle-${runtime.id}`}
                          />
                        ) : null}
                      </>
                    ) : row.docsUrl ? (
                      <Tooltip>
                        <TooltipTrigger
                          render={
                            <a
                              href={row.docsUrl}
                              target="_blank"
                              rel="noopener noreferrer"
                              aria-label={t(($) => $.machine.code_agents_docs)}
                              className="inline-flex size-6 items-center justify-center rounded-md text-muted-foreground transition-colors hover:bg-muted hover:text-foreground"
                            />
                          }
                        >
                          <ExternalLink className="size-3" aria-hidden />
                          <span className="sr-only">
                            {t(($) => $.machine.code_agents_docs)}
                          </span>
                        </TooltipTrigger>
                        <TooltipContent>
                          {t(($) => $.machine.code_agents_docs)}
                        </TooltipContent>
                      </Tooltip>
                    ) : null}
                  </div>
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
        <DialogContent className="sm:max-w-lg">
          <DialogHeader>
            <div className="flex items-center gap-2.5">
              {envTarget ? (
                <span className="flex size-8 shrink-0 items-center justify-center rounded-lg border bg-background">
                  <ProviderLogo
                    provider={envTarget.provider}
                    className="size-4.5"
                  />
                </span>
              ) : null}
              <div className="min-w-0">
                <DialogTitle>
                  {t(($) => $.machine.code_agents_env_dialog_title)}
                </DialogTitle>
                {envTarget ? (
                  <p className="mt-0.5 truncate text-xs text-muted-foreground">
                    {envTarget.label}
                  </p>
                ) : null}
              </div>
            </div>
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
