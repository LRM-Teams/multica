"use client";

import type { ResearchReport, ResearchSource } from "@multica/core/types";
import { Button } from "@multica/ui/components/ui/button";
import { useT } from "../../i18n/use-t";

export function ResearchDeliveryDrawer({
  open,
  onClose,
  report,
  sources,
}: {
  open: boolean;
  onClose: () => void;
  report: ResearchReport | null | undefined;
  sources: ResearchSource[];
}) {
  const { t } = useT("research");
  if (!open) return null;

  const sorted = [...sources].sort((a, b) => b.credibility_weight - a.credibility_weight);

  return (
    <div className="pointer-events-auto absolute bottom-4 left-4 right-4 z-20 mx-auto max-w-2xl overflow-hidden rounded-xl border bg-card/95 shadow-xl backdrop-blur md:left-auto md:right-4 md:w-[420px]">
      <div className="flex items-center justify-between border-b px-3 py-2">
        <div className="text-xs font-medium">{t(($) => $.panel.delivery)}</div>
        <Button type="button" size="sm" variant="ghost" onClick={onClose}>
          {t(($) => $.panel.hide_chat)}
        </Button>
      </div>
      <div className="max-h-[50vh] space-y-4 overflow-y-auto p-3">
        <div>
          <div className="mb-1 text-[10px] font-medium uppercase text-muted-foreground">
            {t(($) => $.panel.sources)}
          </div>
          {sorted.length === 0 ? (
            <p className="text-xs text-muted-foreground">—</p>
          ) : (
            <ul className="space-y-2">
              {sorted.slice(0, 12).map((s) => (
                <li key={s.id} className="rounded-md border px-2 py-1.5">
                  <div className="flex items-center justify-between gap-2">
                    <span className="truncate text-xs font-medium">{s.title || s.url || s.source_class}</span>
                    <span className="shrink-0 text-[10px] text-muted-foreground">
                      {t(($) => $.panel.weight)} {(s.credibility_weight ?? 0).toFixed(2)}
                    </span>
                  </div>
                  {s.summary ? (
                    <p className="mt-0.5 line-clamp-2 text-[11px] text-muted-foreground">{s.summary}</p>
                  ) : null}
                </li>
              ))}
            </ul>
          )}
        </div>
        <div>
          <div className="mb-1 text-[10px] font-medium uppercase text-muted-foreground">
            {t(($) => $.panel.report)}
          </div>
          <div className="whitespace-pre-wrap rounded-md border bg-muted/30 p-2 text-xs leading-relaxed">
            {report?.content_md || "—"}
          </div>
        </div>
      </div>
    </div>
  );
}
