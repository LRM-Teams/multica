"use client";

import { useMemo, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "@multica/core/api";
import { useWorkspaceId } from "@multica/core/hooks";
import {
  researchKeys,
  researchSessionSnapshotOptions,
  type ResearchPresenceMap,
} from "@multica/core/research";
import type { ResearchGraphNode } from "@multica/core/types";
import { Button } from "@multica/ui/components/ui/button";
import { Textarea } from "@multica/ui/components/ui/textarea";
import { Badge } from "@multica/ui/components/ui/badge";
import { Checkbox } from "@multica/ui/components/ui/checkbox";
import { Skeleton } from "@multica/ui/components/ui/skeleton";
import { useT } from "../../i18n/use-t";
import { ExplorationMap } from "./exploration-map";

export function ResearchSessionPage({ sessionId }: { sessionId: string }) {
  const { t } = useT("research");
  const wsId = useWorkspaceId();
  const qc = useQueryClient();
  const { data, isLoading } = useQuery(researchSessionSnapshotOptions(wsId, sessionId));
  const { data: presence = {} } = useQuery<ResearchPresenceMap>({
    queryKey: researchKeys.presence(wsId, sessionId),
    queryFn: () => ({}),
    enabled: !!wsId && !!sessionId,
    staleTime: Infinity,
  });
  const [selected, setSelected] = useState<ResearchGraphNode | null>(null);
  const [body, setBody] = useState("");
  const [createProject, setCreateProject] = useState(true);
  const [createChannel, setCreateChannel] = useState(true);

  const send = useMutation({
    mutationFn: () => api.postResearchMessage(sessionId, { body: body.trim() }),
    onSuccess: () => {
      setBody("");
      void qc.invalidateQueries({ queryKey: researchKeys.snapshot(wsId, sessionId) });
    },
  });

  const confirm = useMutation({
    mutationFn: () => api.confirmResearchSession(sessionId),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: researchKeys.snapshot(wsId, sessionId) });
    },
  });

  const handoff = useMutation({
    mutationFn: () =>
      api.researchSessionHandoff(sessionId, {
        create_project: createProject,
        create_channel: createChannel,
      }),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: researchKeys.snapshot(wsId, sessionId) });
    },
  });

  const sourcesSorted = useMemo(
    () => [...(data?.sources ?? [])].sort((a, b) => b.credibility_weight - a.credibility_weight),
    [data?.sources],
  );

  if (isLoading || !data) {
    return (
      <div className="grid h-full grid-cols-3 gap-0">
        <Skeleton className="h-full" />
        <Skeleton className="h-full" />
        <Skeleton className="h-full" />
      </div>
    );
  }

  const { session, fleet, report, messages } = data;
  const canConfirm = session.status === "awaiting_user_confirm" || session.status === "running";
  const canHandoff = session.status === "completed" || session.status === "awaiting_user_confirm";

  return (
    <div className="grid h-full min-h-0 grid-cols-[minmax(280px,1.2fr)_minmax(280px,1fr)_minmax(260px,0.9fr)]">
      <section className="min-h-0 border-r">
        <ExplorationMap
          nodes={data.nodes}
          edges={data.edges}
          selectedId={selected?.id}
          onSelect={setSelected}
        />
      </section>

      <section className="flex min-h-0 flex-col gap-4 overflow-y-auto border-r p-4">
        <div className="flex items-center justify-between gap-2">
          <div className="min-w-0">
            <h1 className="truncate text-lg font-semibold">{session.title}</h1>
            <p className="truncate text-xs text-muted-foreground">{session.goal}</p>
          </div>
          <Badge variant="secondary">
            {t(($) => $.status[session.status as keyof typeof $.status] ?? session.status)}
          </Badge>
        </div>

        <div>
          <div className="mb-2 text-xs font-medium text-muted-foreground">{t(($) => $.panel.sources)}</div>
          <div className="space-y-2">
            {sourcesSorted.length === 0 ? (
              <p className="text-sm text-muted-foreground">—</p>
            ) : (
              sourcesSorted.map((s) => (
                <div key={s.id} className="rounded-md border px-3 py-2">
                  <div className="flex items-center justify-between gap-2">
                    <div className="truncate text-sm font-medium">{s.title || s.url || s.source_class}</div>
                    <span className="text-[11px] text-muted-foreground">
                      {t(($) => $.panel.weight)} {(s.credibility_weight ?? 0).toFixed(2)}
                    </span>
                  </div>
                  <div className="text-[11px] text-muted-foreground">{s.source_class} · {s.url}</div>
                  {s.summary ? <p className="mt-1 text-xs text-muted-foreground">{s.summary}</p> : null}
                </div>
              ))
            )}
          </div>
        </div>

        <div>
          <div className="mb-2 text-xs font-medium text-muted-foreground">{t(($) => $.panel.report)}</div>
          <div className="prose prose-sm dark:prose-invert max-w-none whitespace-pre-wrap rounded-md border p-3 text-sm">
            {report?.content_md || "—"}
          </div>
        </div>

        {selected ? (
          <div className="rounded-md border bg-muted/30 p-3 text-xs">
            <div className="font-medium">{selected.title}</div>
            <p className="mt-1 text-muted-foreground">{selected.summary}</p>
          </div>
        ) : null}

        <div className="mt-auto flex flex-wrap items-center gap-3 border-t pt-3">
          {canConfirm && session.status !== "completed" ? (
            <Button size="sm" onClick={() => confirm.mutate()} disabled={confirm.isPending}>
              {t(($) => $.panel.confirm)}
            </Button>
          ) : null}
          {canHandoff ? (
            <>
              <label className="flex items-center gap-2 text-xs">
                <Checkbox checked={createProject} onCheckedChange={(v) => setCreateProject(v === true)} />
                {t(($) => $.panel.handoff_project)}
              </label>
              <label className="flex items-center gap-2 text-xs">
                <Checkbox checked={createChannel} onCheckedChange={(v) => setCreateChannel(v === true)} />
                {t(($) => $.panel.handoff_channel)}
              </label>
              <Button
                size="sm"
                variant="secondary"
                disabled={handoff.isPending || (!createProject && !createChannel)}
                onClick={() => handoff.mutate()}
              >
                {t(($) => $.panel.handoff)}
              </Button>
            </>
          ) : null}
        </div>
      </section>

      <section className="flex min-h-0 flex-col">
        <div className="border-b px-3 py-2 text-xs font-medium text-muted-foreground">
          {t(($) => $.panel.chat)}
        </div>
        <div className="border-b px-3 py-2 text-[11px] text-muted-foreground">
          {t(($) => $.panel.fleet)}:{" "}
          {fleet.members
            .filter((m) => m.status !== "archived")
            .map((m) => m.display_name || m.name || m.role)
            .join(" · ")}
        </div>
        {Object.keys(presence).length > 0 ? (
          <div className="space-y-1 border-b px-3 py-2 text-[11px]">
            <div className="font-medium text-muted-foreground">{t(($) => $.panel.presence)}</div>
            {fleet.members
              .filter((m) => presence[m.agent_id]?.activity)
              .map((m) => (
                <div key={m.agent_id} className="truncate text-muted-foreground">
                  <span className="text-foreground">
                    {m.display_name || m.name || m.role}
                  </span>
                  {" · "}
                  {presence[m.agent_id]?.activity}
                </div>
              ))}
          </div>
        ) : null}
        <div className="flex-1 space-y-2 overflow-y-auto p-3">
          {messages.map((m) => (
            <div
              key={m.id}
              className={
                m.sender_type === "user"
                  ? "ml-6 rounded-md bg-primary/10 px-3 py-2 text-sm"
                  : "mr-6 rounded-md bg-muted px-3 py-2 text-sm"
              }
            >
              <div className="mb-1 text-[10px] uppercase text-muted-foreground">{m.sender_type}</div>
              {m.body}
            </div>
          ))}
        </div>
        <div className="space-y-2 border-t p-3">
          <Textarea
            rows={3}
            value={body}
            onChange={(e) => setBody(e.target.value)}
            placeholder={t(($) => $.panel.chat_placeholder)}
          />
          <Button
            size="sm"
            disabled={!body.trim() || send.isPending}
            onClick={() => send.mutate()}
          >
            {t(($) => $.panel.send)}
          </Button>
        </div>
      </section>
    </div>
  );
}
