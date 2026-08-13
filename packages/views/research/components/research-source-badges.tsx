"use client";

import { useState } from "react";
import type { ResearchSource } from "@multica/core/types";
import { Badge } from "@multica/ui/components/ui/badge";
import { Button } from "@multica/ui/components/ui/button";
import { cn } from "@multica/ui/lib/utils";
import { useT } from "../../i18n/use-t";

/** Preview count before 「查看全部」 (LRM-800 / cancelled LRM-782 polish). */
const PREVIEW_LIMIT_OVERLAY = 8;
const PREVIEW_LIMIT_EMBEDDED = 12;

function hostOf(url: string): string {
  try {
    return new URL(url).hostname.replace(/^www\./, "");
  } catch {
    return url.slice(0, 28) || "source";
  }
}

/** Source / confidence list — canvas overlay (legacy) or settings panel (LRM-919). */
export function ResearchSourceBadges({
  sources,
  embedded = false,
}: {
  sources: ResearchSource[];
  embedded?: boolean;
}) {
  const { t } = useT("research");
  const [expanded, setExpanded] = useState(false);
  const previewLimit = embedded ? PREVIEW_LIMIT_EMBEDDED : PREVIEW_LIMIT_OVERLAY;
  const sorted = sources.toSorted(
    (a, b) => (b.credibility_weight ?? 0) - (a.credibility_weight ?? 0),
  );
  const truncated = sorted.length > previewLimit;
  const visible = expanded || !truncated ? sorted : sorted.slice(0, previewLimit);

  if (sorted.length === 0) {
    return embedded ? (
      <p className="px-1 text-xs text-muted-foreground">{t(($) => $.panel.sources_empty)}</p>
    ) : null;
  }

  return (
    <div
      data-testid="research-source-badges"
      className={cn(
        "flex flex-wrap gap-1.5",
        !embedded &&
          "pointer-events-none absolute top-14 right-3 z-10 max-w-[min(420px,46vw)] justify-end",
      )}
    >
      {!embedded ? (
        <span className="pointer-events-none mb-0.5 w-full text-right text-[10px] font-medium tracking-wide text-muted-foreground uppercase">
          {t(($) => $.panel.sources)} · {sources.length}
        </span>
      ) : (
        <p className="mb-1 w-full text-[11px] text-muted-foreground">
          {t(($) => $.panel.sources_hint)}
        </p>
      )}
      {visible.map((s) => (
        <Badge
          key={s.id}
          variant="secondary"
          className={cn(
            "max-w-full truncate border bg-card/90 text-[10px] shadow-sm backdrop-blur",
            !embedded && "pointer-events-auto max-w-[160px]",
            embedded && "max-w-full",
          )}
          title={
            typeof s.credibility_weight === "number"
              ? `${s.title || s.url} (${s.credibility_weight.toFixed(2)})`
              : s.title || s.url
          }
        >
          <span className="truncate">{s.title || hostOf(s.url)}</span>
          {typeof s.credibility_weight === "number" ? (
            <span className="ml-1 text-muted-foreground">
              {s.credibility_weight.toFixed(1)}
            </span>
          ) : null}
        </Badge>
      ))}
      {truncated ? (
        <Button
          type="button"
          variant="ghost"
          size="sm"
          data-testid="research-sources-expand"
          className={cn(
            "h-6 px-2 text-[11px] text-muted-foreground hover:text-foreground",
            !embedded && "pointer-events-auto",
          )}
          onClick={() => setExpanded((v) => !v)}
        >
          {expanded
            ? t(($) => $.panel.sources_collapse)
            : t(($) => $.panel.sources_view_all, { count: sorted.length })}
        </Button>
      ) : null}
    </div>
  );
}
