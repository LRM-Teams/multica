"use client";

import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Database, Loader2 } from "lucide-react";
import { api } from "@multica/core/api";
import {
  Popover,
  PopoverContent,
  PopoverDescription,
  PopoverTitle,
  PopoverTrigger,
} from "@multica/ui/components/ui/popover";
import { useT } from "../../i18n/use-t";

export function GraphMemoryCitationBadge({ workspaceId, messageId, count }: { workspaceId: string; messageId: string; count: number }) {
  const { t } = useT("channels");
  const [open, setOpen] = useState(false);
  const { data, isLoading } = useQuery({
    queryKey: ["graph-memory", "message-citations", workspaceId, messageId],
    queryFn: () => api.getGraphMemoryMessageCitations(workspaceId, messageId),
    enabled: open && Boolean(workspaceId && messageId),
    staleTime: Number.POSITIVE_INFINITY,
  });
  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverTrigger
        render={
          <button
            type="button"
            className="mt-1 inline-flex h-6 items-center gap-1 rounded-full border border-border/70 bg-muted/45 px-2 text-[11px] font-medium text-muted-foreground hover:bg-muted hover:text-foreground"
            aria-label={`${t(($) => $.graph_memory.citations)} (${count})`}
          >
            <Database className="size-3" aria-hidden />
            {t(($) => $.graph_memory.citations)} · {count}
          </button>
        }
      />
      <PopoverContent className="w-[min(24rem,calc(100vw-2rem))] space-y-3" align="start">
        <PopoverTitle>{t(($) => $.graph_memory.citations)}</PopoverTitle>
        <PopoverDescription className="sr-only">{t(($) => $.graph_memory.citations)}</PopoverDescription>
        {isLoading ? (
          <div className="flex items-center gap-2 text-xs text-muted-foreground"><Loader2 className="size-3.5 animate-spin" />{t(($) => $.graph_memory.citation_loading)}</div>
        ) : (data?.items.length ?? 0) === 0 ? (
          <p className="text-xs text-muted-foreground">{t(($) => $.graph_memory.citation_empty)}</p>
        ) : (
          <div className="max-h-80 space-y-2 overflow-y-auto">
            {data?.items.map((citation) => (
              <article key={citation.id} className="rounded-md border border-border/70 bg-muted/25 p-2.5">
                <div className="flex items-center justify-between gap-2">
                  <p className="truncate text-xs font-medium text-foreground">{citation.title || citation.node_id}</p>
                  <span className="shrink-0 text-[10px] text-muted-foreground">{t(($) => $.graph_memory.citation_version, { version: citation.graph_version })}</span>
                </div>
                <p className="mt-1 line-clamp-4 whitespace-pre-wrap text-xs leading-relaxed text-muted-foreground">{citation.excerpt || citation.first_paragraph}</p>
                <p className="mt-1 truncate font-mono text-[10px] text-muted-foreground/80">{citation.node_id} · {citation.content_hash}</p>
              </article>
            ))}
          </div>
        )}
      </PopoverContent>
    </Popover>
  );
}
