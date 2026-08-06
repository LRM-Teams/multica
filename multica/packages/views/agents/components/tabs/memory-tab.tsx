"use client";

import { useEffect, useMemo, useState } from "react";
import { Brain, FileText } from "lucide-react";
import { useQuery } from "@tanstack/react-query";
import { agentMemoryOptions } from "@multica/core/agents/queries";
import { useWorkspaceId } from "@multica/core/hooks";
import { Markdown } from "../../../common/markdown";
import { useT } from "../../../i18n";
import type { Agent } from "@multica/core/types";

export function MemoryTab({ agent }: { agent: Agent }) {
  const { t } = useT("agents");
  const wsId = useWorkspaceId();
  const { data: memories = [], isLoading } = useQuery(
    agentMemoryOptions(wsId, agent.id),
  );
  const [selectedId, setSelectedId] = useState<string | null>(null);

  useEffect(() => {
    if (memories.length === 0) {
      setSelectedId(null);
      return;
    }
    if (!selectedId || !memories.some((memory) => memory.id === selectedId)) {
      setSelectedId(memories[0]?.id ?? null);
    }
  }, [memories, selectedId]);

  const selected = useMemo(
    () => memories.find((memory) => memory.id === selectedId) ?? memories[0] ?? null,
    [memories, selectedId],
  );

  return (
    <div className="flex h-full min-h-0 flex-col gap-4">
      <p className="shrink-0 text-xs text-muted-foreground">
        {t(($) => $.tab_body.memory.intro)}
      </p>

      {isLoading ? (
        <div className="rounded-lg border border-dashed py-10 text-center text-sm text-muted-foreground">
          {t(($) => $.tab_body.memory.loading)}
        </div>
      ) : memories.length === 0 ? (
        <div className="flex flex-col items-center justify-center rounded-lg border border-dashed py-12">
          <Brain className="h-8 w-8 text-muted-foreground/40" />
          <p className="mt-3 text-sm text-muted-foreground">
            {t(($) => $.tab_body.memory.empty_title)}
          </p>
          <p className="mt-1 max-w-sm text-center text-xs text-muted-foreground">
            {t(($) => $.tab_body.memory.empty_hint)}
          </p>
        </div>
      ) : memories.length === 1 && selected ? (
        <MemoryDocument memory={selected} />
      ) : selected ? (
        <div className="grid min-h-0 flex-1 gap-4 lg:grid-cols-[240px_minmax(0,1fr)]">
          <div className="min-h-0 overflow-y-auto rounded-lg border bg-muted/20 p-1.5">
            {memories.map((memory) => {
              const active = memory.id === selected.id;
              return (
                <button
                  key={memory.id}
                  type="button"
                  onClick={() => setSelectedId(memory.id)}
                  className={`flex w-full items-start gap-2 rounded-md px-2.5 py-2 text-left transition-colors ${
                    active
                      ? "bg-background text-foreground shadow-sm"
                      : "text-muted-foreground hover:bg-background/70 hover:text-foreground"
                  }`}
                >
                  <FileText className="mt-0.5 h-3.5 w-3.5 shrink-0" />
                  <span className="min-w-0 flex-1">
                    <span className="block truncate text-sm font-medium">
                      {memory.name}
                    </span>
                    <span className="mt-0.5 block truncate text-[11px]">
                      {memory.sync_key}
                    </span>
                  </span>
                </button>
              );
            })}
          </div>
          <MemoryDocument memory={selected} />
        </div>
      ) : null}
    </div>
  );
}

function MemoryDocument({
  memory,
}: {
  memory: {
    name: string;
    sync_key: string;
    content: string;
    updated_at: string;
  };
}) {
  const { t } = useT("agents");
  return (
    <article className="min-h-0 overflow-hidden rounded-lg border bg-background">
      <header className="border-b px-4 py-3">
        <div className="flex min-w-0 items-center gap-2">
          <FileText className="h-4 w-4 shrink-0 text-muted-foreground" />
          <h3 className="truncate text-sm font-semibold">{memory.name}</h3>
        </div>
        <div className="mt-1 flex flex-wrap items-center gap-x-3 gap-y-1 text-[11px] text-muted-foreground">
          <span>{memory.sync_key}</span>
          <span>{t(($) => $.tab_body.memory.updated_at, { value: formatMemoryTime(memory.updated_at) })}</span>
        </div>
      </header>
      <div className="max-h-[calc(100vh-22rem)] overflow-y-auto p-4 sm:p-5">
        {memory.content.trim() ? (
          <Markdown mode="full">{memory.content}</Markdown>
        ) : (
          <p className="text-sm text-muted-foreground">
            {t(($) => $.tab_body.memory.empty_content)}
          </p>
        )}
      </div>
    </article>
  );
}

function formatMemoryTime(value: string) {
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return value;
  return date.toLocaleString();
}
