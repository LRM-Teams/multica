"use client";

import type { ReactNode } from "react";
import { ExternalLink } from "lucide-react";
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
 * LRM-922 / LRM-863 / LRM-960 — Code agents inventory:
 * section title + short count + provider grid.
 * Install guide only on supported-not-installed cards (no long summary strip).
 */
export function MachineCodeAgentsSection({
  machine,
}: {
  machine: RuntimeMachine;
}) {
  const { t } = useT("runtimes");
  const { installed, notInstalled } = partitionMachineCodeAgents(
    machine.runtimes,
  );
  const rows = [...installed, ...notInstalled];
  const installedCount = installed.length;
  const supportedMore = notInstalled.length;

  return (
    <section>
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
              return (
                <article
                  key={row.id}
                  className="min-h-[96px] p-3.5 md:min-h-[110px]"
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
                      </span>
                    </p>
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
