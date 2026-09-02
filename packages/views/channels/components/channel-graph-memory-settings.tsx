"use client";

import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "@multica/core/api";
import type { GraphMemoryChannelModeOverride } from "@multica/core/types";
import { Button } from "@multica/ui/components/ui/button";
import { Badge } from "@multica/ui/components/ui/badge";
import { showErrorToast } from "@multica/ui/lib/error-toast";
import { useT } from "../../i18n";

export function ChannelGraphMemorySettings({ wsId, channelId, disabled }: { wsId: string; channelId: string; disabled: boolean }) {
  const { t } = useT("channels");
  const queryClient = useQueryClient();
  const key = ["channels", channelId, "graph-memory-mode"] as const;
  const { data, isLoading } = useQuery({ queryKey: key, queryFn: () => api.getGraphMemoryChannelMode(wsId, channelId) });
  // Migration phase (plan Task 16): shown only when the channel rebound
  // across projects through the binding service.
  const { data: lineage } = useQuery({
    queryKey: ["channels", channelId, "graph-memory-lineage"] as const,
    queryFn: () => api.getGraphMemoryChannelLineage(wsId, channelId),
  });
  const [draft, setDraft] = useState<GraphMemoryChannelModeOverride | null>(null);
  const selected = draft ?? data?.override ?? "inherit";
  const update = useMutation({
    mutationFn: () => api.updateGraphMemoryChannelMode(wsId, channelId, selected),
    onSuccess: async () => {
      setDraft(null);
      await queryClient.invalidateQueries({ queryKey: key });
    },
    onError: (error) => showErrorToast(error instanceof Error ? error.message : t(($) => $.graph_memory.update_failed)),
  });
  const reset = useMutation({
    mutationFn: () => api.resetGraphMemoryChannelAgent(wsId, channelId),
    onSuccess: async () => queryClient.invalidateQueries({ queryKey: key }),
    onError: (error) => showErrorToast(error instanceof Error ? error.message : t(($) => $.graph_memory.reset_failed)),
  });

  return (
    <section className="border-t p-3 md:p-4" data-testid="channel-graph-memory-settings">
      <div className="mb-2 flex items-center justify-between gap-2">
        <div>
          <p className="text-sm font-semibold">{t(($) => $.graph_memory.title)}</p>
          <p className="text-xs text-muted-foreground">{t(($) => $.graph_memory.description)}</p>
        </div>
        {data ? <Badge variant={data.status === "blocked" ? "destructive" : "secondary"}>{data.status}</Badge> : null}
      </div>
      <div className="grid grid-cols-3 gap-2">
        {(["inherit", "agent", "inject"] as const).map((mode) => (
          <Button key={mode} type="button" size="sm" variant={selected === mode ? "default" : "outline"} disabled={disabled || isLoading || update.isPending} onClick={() => setDraft(mode)}>
            {t(($) => $.graph_memory[mode])}
          </Button>
        ))}
      </div>
      {data?.blocked_reason ? <p className="mt-2 text-xs text-destructive">{data.blocked_reason}</p> : null}
      {data ? <p className="mt-2 text-xs text-muted-foreground">{t(($) => $.graph_memory.effective)}: {data.effective_mode}</p> : null}
      {lineage?.migration ? (
        <div className="mt-2 rounded-md border border-border/70 bg-muted/25 p-2 text-xs text-muted-foreground" data-testid="channel-graph-memory-migration">
          <p className="text-xs font-medium text-foreground">{t(($) => $.graph_memory.migration_title)}</p>
          <div className="mt-1 flex flex-wrap gap-x-3 gap-y-1">
            <span>{t(($) => $.graph_memory.migration_phase, { phase: lineage.migration.phase || "—" })}</span>
            <span>{t(($) => $.graph_memory.migration_generation, { generation: lineage.migration.binding_generation })}</span>
            <span>{t(($) => $.graph_memory.migration_copied, { count: lineage.migration.copied_atoms })}</span>
          </div>
        </div>
      ) : null}
      <div className="mt-3 flex gap-2">
        <Button size="sm" variant="outline" disabled={disabled || update.isPending || draft === null} onClick={() => update.mutate()}>{t(($) => $.graph_memory.save)}</Button>
        <Button size="sm" variant="ghost" disabled={disabled || reset.isPending || data?.effective_mode !== "agent"} onClick={() => reset.mutate()}>{t(($) => $.graph_memory.reset)}</Button>
      </div>
    </section>
  );
}
