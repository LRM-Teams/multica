"use client";

import { useRef, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "@multica/core/api";
import { useWorkspaceId } from "@multica/core/hooks";
import { useWorkspacePaths } from "@multica/core/paths";
import {
  researchFleetOptions,
  researchKeys,
  researchSessionListOptions,
} from "@multica/core/research";
import type { ResearchSession } from "@multica/core/types";
import { Button } from "@multica/ui/components/ui/button";
import { Textarea } from "@multica/ui/components/ui/textarea";
import { Skeleton } from "@multica/ui/components/ui/skeleton";
import { AlertCircle } from "lucide-react";
import { useNavigation } from "../../navigation/context";
import { useT } from "../../i18n/use-t";
import { ResearchEmptyState } from "./research-empty-state";
import { ResearchSessionRow } from "./research-session-row";

/** LRM-789: terminal sessions fall under the 已完成 group; everything else is 进行中. */
const DONE_STATUSES = new Set(["completed", "archived"]);

export function ResearchListPage() {
  const { t } = useT("research");
  const wsId = useWorkspaceId();
  const paths = useWorkspacePaths();
  const nav = useNavigation();
  const qc = useQueryClient();
  const [goal, setGoal] = useState("");
  const goalInputRef = useRef<HTMLTextAreaElement>(null);

  useQuery(researchFleetOptions(wsId));
  const { data, isLoading, isError, error, refetch } = useQuery(
    researchSessionListOptions(wsId),
  );

  const create = useMutation({
    mutationFn: () => api.createResearchSession({ goal: goal.trim() }),
    onSuccess: (res) => {
      // Seed snapshot from kickoff payload so the session page paints a busy graph
      // without waiting on the first GET / WS round-trip.
      qc.setQueryData(researchKeys.snapshot(wsId, res.session.id), {
        session: res.session,
        fleet: res.fleet,
        nodes: res.nodes ?? [],
        edges: res.edges ?? [],
        sources: [],
        report: null,
        evals: [],
        messages: res.messages ?? [],
      });
      void qc.invalidateQueries({ queryKey: researchKeys.sessions(wsId) });
      nav.push(paths.researchDetail(res.session.id));
    },
  });

  const sessions = data?.sessions ?? [];
  const inProgress = sessions.filter((s) => !DONE_STATUSES.has(s.status));
  const completed = sessions.filter((s) => DONE_STATUSES.has(s.status));

  const focusComposer = () => {
    const el = goalInputRef.current;
    if (!el) return;
    el.scrollIntoView({ block: "center", behavior: "smooth" });
    el.focus({ preventScroll: true });
  };

  const fillComposer = (text: string) => {
    setGoal(text);
    // Defer focus so the controlled value paints before the caret moves.
    queueMicrotask(focusComposer);
  };

  const renderRow = (s: ResearchSession) => (
    <ResearchSessionRow key={s.id} session={s} href={paths.researchDetail(s.id)} />
  );

  return (
    <div className="flex h-full flex-col gap-6 p-6">
      <div className="flex items-center justify-between">
        <h1 className="text-xl font-semibold">{t(($) => $.title)}</h1>
      </div>

      <div className="max-w-2xl space-y-3 rounded-lg border p-4">
        <Textarea
          ref={goalInputRef}
          value={goal}
          onChange={(e) => setGoal(e.target.value)}
          placeholder={t(($) => $.goal_placeholder)}
          rows={3}
        />
        <Button
          disabled={!goal.trim() || create.isPending}
          onClick={() => create.mutate()}
        >
          {t(($) => $.start)}
        </Button>
      </div>

      {isLoading ? (
        <div className="space-y-2" aria-busy="true" aria-label={t(($) => $.list.loading)}>
          {Array.from({ length: 4 }, (_, i) => (
            <Skeleton key={i} className="h-16 w-full" />
          ))}
        </div>
      ) : isError ? (
        <div
          role="alert"
          className="flex flex-col items-center justify-center gap-3 rounded-lg border border-destructive/40 bg-destructive/5 px-6 py-12 text-center"
        >
          <AlertCircle className="size-6 text-destructive" />
          <p className="text-sm text-destructive">
            {error instanceof Error && error.message
              ? error.message
              : t(($) => $.list.load_failed)}
          </p>
          <Button variant="outline" size="sm" onClick={() => refetch()}>
            {t(($) => $.list.retry)}
          </Button>
        </div>
      ) : sessions.length === 0 ? (
        <ResearchEmptyState
          onSelectExample={fillComposer}
          onStart={focusComposer}
        />
      ) : (
        <div className="space-y-6">
          {inProgress.length > 0 && (
            <section>
              <h2 className="px-1 text-xs font-medium text-muted-foreground">
                {t(($) => $.groups.in_progress)}
              </h2>
              <div className="mt-2 space-y-2">{inProgress.map(renderRow)}</div>
            </section>
          )}
          {completed.length > 0 && (
            <section>
              <h2 className="px-1 text-xs font-medium text-muted-foreground">
                {t(($) => $.groups.completed)}
              </h2>
              <div className="mt-2 space-y-2">{completed.map(renderRow)}</div>
            </section>
          )}
        </div>
      )}
    </div>
  );
}
