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
 * LRM-922 / LRM-863 / LRM-960 / LRM-1071 / LRM-1108 — Code agents inventory:
 * section title + provider grid. Installed status is a green dot + muted
 * version (A2). A2.1 drops the
 * section-header installed/supported count.
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
                    <div className="mt-auto pt-3">
                      <span className="inline-flex min-w-0 items-center gap-1.5 text-[11px] text-muted-foreground">
                        <span
                          className="inline-block h-1.5 w-1.5 shrink-0 rounded-full bg-online"
                          aria-hidden
                        />
                        {versionLabel ? (
                          <span className="tabular-nums">{versionLabel}</span>
                        ) : null}
                      </span>
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
    </section>
  );
}
