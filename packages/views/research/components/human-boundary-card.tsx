"use client";

import { cn } from "@multica/ui/lib/utils";
import { useT } from "../../i18n/use-t";
import type { HumanBoundaryModel } from "../lib/m2-visibility";

export function HumanBoundaryCard({
  model,
  className,
  embedded = false,
}: {
  model: HumanBoundaryModel;
  className?: string;
  /** Report-reader embed (LRM-880 coexist). */
  embedded?: boolean;
}) {
  const { t } = useT("research");

  return (
    <section
      data-testid="human-boundary-card"
      className={cn(
        "rounded-[10px] border bg-card",
        embedded ? "p-4" : "p-3 shadow-sm",
        className,
      )}
    >
      <header className="mb-2 flex items-center gap-2">
        <h3
          className={cn(
            "font-semibold text-foreground",
            embedded ? "text-base" : "text-sm",
          )}
        >
          {t(($) => $.m2.boundary_title)}
        </h3>
        <span className="rounded-md border px-1.5 py-0.5 text-[10px] text-muted-foreground">
          {t(($) => $.m2.boundary_chip)}
        </span>
      </header>

      {model.empty ? (
        <p className="text-xs text-muted-foreground">{t(($) => $.m2.boundary_empty)}</p>
      ) : (
        <div className="space-y-2.5 text-[12px] leading-relaxed">
          <div>
            <div className="mb-0.5 text-[10px] font-medium tracking-wide text-muted-foreground uppercase">
              {t(($) => $.m2.ai_ceiling)}
            </div>
            <p className="text-foreground">{model.aiCeiling || "—"}</p>
          </div>
          <div>
            <div className="mb-0.5 text-[10px] font-medium tracking-wide text-[color:var(--role-human)] uppercase">
              {t(($) => $.m2.must_human)}
            </div>
            <p className="text-foreground">{model.mustHuman || "—"}</p>
          </div>
          {model.matrix.length > 0 ? (
            <div className="overflow-x-auto rounded-md border">
              <table className="w-full text-left text-[11px]">
                <thead className="bg-muted/40 text-muted-foreground">
                  <tr>
                    <th className="px-2 py-1.5 font-medium">{t(($) => $.m2.col_human)}</th>
                    <th className="px-2 py-1.5 font-medium">{t(($) => $.m2.col_ai)}</th>
                  </tr>
                </thead>
                <tbody>
                  {model.matrix.map((row, i) => (
                    <tr key={i} className="border-t">
                      <td className="px-2 py-1.5">{row.human}</td>
                      <td className="px-2 py-1.5 text-muted-foreground">{row.ai}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          ) : null}
        </div>
      )}
    </section>
  );
}
