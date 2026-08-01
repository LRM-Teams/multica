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
    <h2 className="mb-2 px-1 text-[11px] font-semibold uppercase tracking-wide text-muted-foreground">
      {children}
    </h2>
  );
}

function GroupTitle({ children }: { children: ReactNode }) {
  return (
    <div className="border-b px-4 py-2 text-[11px] font-medium text-muted-foreground">
      {children}
    </div>
  );
}

export function MachineCodeAgentsSection({
  machine,
}: {
  machine: RuntimeMachine;
}) {
  const { t } = useT("runtimes");
  const { installed, notInstalled } = partitionMachineCodeAgents(
    machine.runtimes,
  );

  return (
    <section>
      <SectionTitle>{t(($) => $.machine.code_agents_section)}</SectionTitle>
      <div className="overflow-hidden rounded-xl border bg-card">
        <GroupTitle>{t(($) => $.machine.code_agents_installed)}</GroupTitle>
        {installed.length === 0 ? (
          <p className="px-4 py-3 text-sm text-muted-foreground">
            {t(($) => $.machine.code_agents_installed_empty)}
          </p>
        ) : (
          <ul>
            {installed.map((row, idx) => (
              <li
                key={row.id}
                className={cn(
                  "flex items-center gap-3 px-4 py-2.5",
                  idx < installed.length - 1 && "border-b",
                )}
              >
                <ProviderLogo provider={row.id} className="h-4 w-4 shrink-0" />
                <span className="min-w-0 flex-1 truncate text-sm font-medium">
                  {row.label}
                </span>
                {row.version ? (
                  <span className="shrink-0 font-mono text-[11px] text-muted-foreground">
                    {t(($) => $.machine.version_prefix, {
                      version: row.version,
                    })}
                  </span>
                ) : null}
              </li>
            ))}
          </ul>
        )}

        {notInstalled.length > 0 ? (
          <>
            <GroupTitle>
              {t(($) => $.machine.code_agents_not_installed)}
            </GroupTitle>
            <ul>
              {notInstalled.map((row, idx) => (
                <li
                  key={row.id}
                  className={cn(
                    "flex items-center gap-3 px-4 py-2.5 text-muted-foreground",
                    idx < notInstalled.length - 1 && "border-b",
                  )}
                >
                  <span className="shrink-0 opacity-40 grayscale">
                    <ProviderLogo provider={row.id} className="h-4 w-4" />
                  </span>
                  <span className="min-w-0 flex-1 truncate text-sm">
                    {row.label}
                  </span>
                  {row.docsUrl ? (
                    <a
                      href={row.docsUrl}
                      target="_blank"
                      rel="noopener noreferrer"
                      className="inline-flex shrink-0 items-center gap-1 text-xs text-muted-foreground underline-offset-2 hover:text-foreground hover:underline"
                    >
                      {t(($) => $.machine.code_agents_docs)}
                      <ExternalLink className="h-3 w-3" aria-hidden />
                    </a>
                  ) : null}
                </li>
              ))}
            </ul>
          </>
        ) : null}
      </div>
    </section>
  );
}
