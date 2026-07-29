"use client";

import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "@multica/core/api";
import { useWorkspaceId } from "@multica/core/hooks";
import { useWorkspacePaths } from "@multica/core/paths";
import {
  researchFleetOptions,
  researchKeys,
  researchSessionListOptions,
} from "@multica/core/research";
import { Button } from "@multica/ui/components/ui/button";
import { Textarea } from "@multica/ui/components/ui/textarea";
import { Badge } from "@multica/ui/components/ui/badge";
import { Skeleton } from "@multica/ui/components/ui/skeleton";
import { useNavigation } from "../../navigation/context";
import { useT } from "../../i18n/use-t";
import { AppLink } from "../../navigation/app-link";

export function ResearchListPage() {
  const { t } = useT("research");
  const wsId = useWorkspaceId();
  const paths = useWorkspacePaths();
  const nav = useNavigation();
  const qc = useQueryClient();
  const [goal, setGoal] = useState("");

  useQuery(researchFleetOptions(wsId));
  const { data, isLoading } = useQuery(researchSessionListOptions(wsId));

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

  return (
    <div className="flex h-full flex-col gap-6 p-6">
      <div className="flex items-center justify-between">
        <h1 className="text-xl font-semibold">{t(($) => $.title)}</h1>
      </div>

      <div className="max-w-2xl space-y-3 rounded-lg border p-4">
        <Textarea
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

      <div className="space-y-2">
        {isLoading ? (
          <Skeleton className="h-16 w-full" />
        ) : sessions.length === 0 ? (
          <div className="text-sm text-muted-foreground">
            <div className="font-medium text-foreground">{t(($) => $.empty_title)}</div>
            <p>{t(($) => $.empty_desc)}</p>
          </div>
        ) : (
          sessions.map((s) => (
            <AppLink
              key={s.id}
              href={paths.researchDetail(s.id)}
              className="flex items-center justify-between rounded-md border px-4 py-3 hover:bg-accent/40"
            >
              <div className="min-w-0">
                <div className="truncate font-medium">{s.title || s.goal}</div>
                <div className="truncate text-xs text-muted-foreground">{s.goal}</div>
              </div>
              <Badge variant="secondary">
                {t(($) => $.status[s.status as keyof typeof $.status] ?? s.status)}
              </Badge>
            </AppLink>
          ))
        )}
      </div>
    </div>
  );
}
