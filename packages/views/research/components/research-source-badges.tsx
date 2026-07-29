"use client";

import type { ResearchSource } from "@multica/core/types";
import { Badge } from "@multica/ui/components/ui/badge";
import { useT } from "../../i18n/use-t";

function hostOf(url: string): string {
  try {
    return new URL(url).hostname.replace(/^www\./, "");
  } catch {
    return url.slice(0, 28) || "source";
  }
}

/** Compact source strip overlaid on the canvas — density without opening delivery drawer. */
export function ResearchSourceBadges({ sources }: { sources: ResearchSource[] }) {
  const { t } = useT("research");
  const top = sources
    .toSorted((a, b) => (b.credibility_weight ?? 0) - (a.credibility_weight ?? 0))
    .slice(0, 8);

  if (top.length === 0) return null;

  return (
    <div className="pointer-events-none absolute right-3 top-14 z-10 flex max-w-[min(420px,46vw)] flex-wrap justify-end gap-1.5">
      <span className="pointer-events-none mb-0.5 w-full text-right text-[10px] font-medium uppercase tracking-wide text-muted-foreground">
        {t(($) => $.panel.sources)} · {sources.length}
      </span>
      {top.map((s) => (
        <Badge
          key={s.id}
          variant="secondary"
          className="pointer-events-auto max-w-[160px] truncate border bg-card/90 text-[10px] shadow-sm backdrop-blur"
          title={`${s.title || s.url} (${(s.credibility_weight ?? 0).toFixed(2)})`}
        >
          <span className="truncate">{s.title || hostOf(s.url)}</span>
          <span className="ml-1 text-muted-foreground">{(s.credibility_weight ?? 0).toFixed(1)}</span>
        </Badge>
      ))}
    </div>
  );
}
