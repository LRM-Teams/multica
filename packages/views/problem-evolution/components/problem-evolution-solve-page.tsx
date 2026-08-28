"use client";

import { useState, type ChangeEvent } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Loader2, Plus, Sparkles } from "lucide-react";
import { Button } from "@multica/ui/components/ui/button";
import { Input } from "@multica/ui/components/ui/input";
import { Textarea } from "@multica/ui/components/ui/textarea";
import { Badge } from "@multica/ui/components/ui/badge";
import { showErrorToast } from "@multica/ui/lib/error-toast";
import { api } from "@multica/core/api";
import { useWorkspaceId } from "@multica/core/hooks";
import {
  problemEvolutionKeys,
  problemEvolutionRunListOptions,
} from "@multica/core/problem-evolution";
import type { ProblemEvolutionRun } from "@multica/core/problem-evolution";
import { useT } from "../../i18n/use-t";

/**
 * Run list and creation for solve runs. A new run is a draft: it carries the
 * problem statement only, and cannot start until its scoring contract is
 * frozen on the detail page.
 */
export function ProblemEvolutionSolvePage({
  onOpenRun,
}: {
  onOpenRun?: (runId: string) => void;
}) {
  const { t } = useT("problem-evolution");
  const workspaceId = useWorkspaceId();
  const queryClient = useQueryClient();
  const [composing, setComposing] = useState(false);
  const [title, setTitle] = useState("");
  const [statement, setStatement] = useState("");
  const [maxModelCalls, setMaxModelCalls] = useState("100");
  const [maxCostUSD, setMaxCostUSD] = useState("");

  const runsQuery = useQuery(problemEvolutionRunListOptions(workspaceId ?? ""));

  const createRun = useMutation({
    mutationFn: () =>
      api.createProblemEvolutionRun({
        mode: "solution",
        title: title.trim(),
        problem_spec: { statement: statement.trim(), artifact_type: "markdown" },
        artifact_type: "markdown",
        stop_config: {
          max_model_calls: Math.max(1, Number(maxModelCalls) || 100),
          // Empty means unlimited; the server stores that as zero.
          max_cost_usd: Math.max(0, Number(maxCostUSD) || 0),
        },
      }),
    onSuccess: (run) => {
      setComposing(false);
      setTitle("");
      setStatement("");
      setMaxModelCalls("100");
      setMaxCostUSD("");
      if (workspaceId) {
        void queryClient.invalidateQueries({
          queryKey: problemEvolutionKeys.runs(workspaceId),
        });
      }
      onOpenRun?.(run.id);
    },
    onError: () => showErrorToast(t(($) => $.errors.createFailed)),
  });

  const runs = runsQuery.data ?? [];

  return (
    <div className="flex h-full flex-col gap-6 p-6">
      <header className="flex items-start justify-between gap-4">
        <div className="space-y-1">
          <h1 className="flex items-center gap-2 text-xl font-semibold">
            <Sparkles className="size-5" aria-hidden />
            {t(($) => $.title)}
          </h1>
          <p className="text-muted-foreground text-sm">{t(($) => $.subtitle)}</p>
        </div>
        <Button onClick={() => setComposing((open) => !open)}>
          <Plus className="size-4" aria-hidden />
          {t(($) => $.newRun)}
        </Button>
      </header>

      {composing ? (
        <section className="space-y-3 rounded-lg border p-4">
          <h2 className="text-sm font-medium">{t(($) => $.create.title)}</h2>
          <div className="space-y-2">
            <label className="text-muted-foreground text-xs" htmlFor="pe-title">
              {t(($) => $.create.runTitle)}
            </label>
            <Input
              id="pe-title"
              value={title}
              placeholder={t(($) => $.create.runTitlePlaceholder)}
              onChange={(event: ChangeEvent<HTMLInputElement>) => setTitle(event.target.value)}
            />
          </div>
          <div className="space-y-2">
            <label className="text-muted-foreground text-xs" htmlFor="pe-statement">
              {t(($) => $.create.statement)}
            </label>
            <Textarea
              id="pe-statement"
              rows={6}
              value={statement}
              placeholder={t(($) => $.create.statementPlaceholder)}
              onChange={(event: ChangeEvent<HTMLTextAreaElement>) => setStatement(event.target.value)}
            />
          </div>
          <div className="grid gap-3 sm:grid-cols-2">
            <div className="space-y-2">
              <label className="text-muted-foreground text-xs" htmlFor="pe-max-model-calls">
                {t(($) => $.create.maxModelCalls)}
              </label>
              <Input
                id="pe-max-model-calls"
                type="number"
                min={1}
                step={1}
                value={maxModelCalls}
                onChange={(event: ChangeEvent<HTMLInputElement>) =>
                  setMaxModelCalls(event.target.value)
                }
              />
              <p className="text-muted-foreground text-xs">
                {t(($) => $.create.maxModelCallsHelp)}
              </p>
            </div>
            <div className="space-y-2">
              <label className="text-muted-foreground text-xs" htmlFor="pe-max-cost">
                {t(($) => $.create.maxCostUSD)}
              </label>
              <Input
                id="pe-max-cost"
                type="number"
                min={0}
                step="0.01"
                value={maxCostUSD}
                placeholder={t(($) => $.create.unlimited)}
                onChange={(event: ChangeEvent<HTMLInputElement>) =>
                  setMaxCostUSD(event.target.value)
                }
              />
              <p className="text-muted-foreground text-xs">
                {t(($) => $.create.maxCostHelp)}
              </p>
            </div>
          </div>
          <div className="flex justify-end gap-2">
            <Button variant="ghost" onClick={() => setComposing(false)}>
              {t(($) => $.create.cancel)}
            </Button>
            <Button
              disabled={statement.trim() === "" || createRun.isPending}
              onClick={() => createRun.mutate()}
            >
              {createRun.isPending ? (
                <Loader2 className="size-4 animate-spin" aria-hidden />
              ) : null}
              {t(($) => $.create.submit)}
            </Button>
          </div>
        </section>
      ) : null}

      {runsQuery.isLoading ? (
        <div className="text-muted-foreground flex items-center gap-2 text-sm">
          <Loader2 className="size-4 animate-spin" aria-hidden />
        </div>
      ) : runs.length === 0 ? (
        <section className="rounded-lg border border-dashed p-8 text-center">
          <h2 className="text-sm font-medium">{t(($) => $.emptyTitle)}</h2>
          <p className="text-muted-foreground mt-1 text-sm">{t(($) => $.emptyBody)}</p>
        </section>
      ) : (
        <ul className="divide-y rounded-lg border">
          {runs.map((run) => (
            <li key={run.id}>
              <button
                type="button"
                className="hover:bg-muted/50 flex w-full items-center justify-between gap-4 p-4 text-left"
                onClick={() => onOpenRun?.(run.id)}
              >
                <span className="min-w-0">
                  <span className="block truncate text-sm font-medium">
                    {run.title || run.id}
                  </span>
                  <span className="text-muted-foreground block text-xs">
                    {run.created_at}
                  </span>
                </span>
                <ProblemEvolutionStatusBadge run={run} />
              </button>
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}

export function ProblemEvolutionStatusBadge({ run }: { run: ProblemEvolutionRun }) {
  const { t } = useT("problem-evolution");
  const label = statusLabel(run.status, t);
  const variant =
    run.status === "failed"
      ? "destructive"
      : run.status === "completed"
        ? "default"
        : "secondary";
  return <Badge variant={variant}>{label}</Badge>;
}

type StatusTranslator = ReturnType<typeof useT<"problem-evolution">>["t"];

function statusLabel(status: string, t: StatusTranslator): string {
  switch (status) {
    case "draft":
      return t(($) => $.status.draft);
    case "validating_evaluator":
      return t(($) => $.status.validating_evaluator);
    case "ready":
      return t(($) => $.status.ready);
    case "queued":
      return t(($) => $.status.queued);
    case "running":
      return t(($) => $.status.running);
    case "synthesizing":
      return t(($) => $.status.synthesizing);
    case "stopping":
      return t(($) => $.status.stopping);
    case "completed":
      return t(($) => $.status.completed);
    case "cancelled":
      return t(($) => $.status.cancelled);
    case "failed":
      return t(($) => $.status.failed);
    default:
      return status;
  }
}
