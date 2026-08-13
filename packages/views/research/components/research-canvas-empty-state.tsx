"use client";

import { useMutation, useQueryClient } from "@tanstack/react-query";
import { api } from "@multica/core/api";
import { useWorkspaceId } from "@multica/core/hooks";
import { useWorkspacePaths } from "@multica/core/paths";
import { researchKeys } from "@multica/core/research";
import { Button } from "@multica/ui/components/ui/button";
import { cn } from "@multica/ui/lib/utils";
import { useT } from "../../i18n/use-t";
import { useNavigation } from "../../navigation/context";

/** Mini graph preview — same family as list empty (LRM-783), session-scoped (LRM-979). */
function EmptyMiniCanvas({
  goalLabel,
  probeLabel,
  sourceLabel,
}: {
  goalLabel: string;
  probeLabel: string;
  sourceLabel: string;
}) {
  return (
    <div
      aria-hidden
      className="relative mb-4 h-28 w-[270px] rounded-xl border border-border/60 bg-canvas-bg sm:h-32 sm:w-[300px]"
      style={{
        backgroundImage:
          "radial-gradient(circle, color-mix(in oklab, var(--foreground) 10%, transparent) 1px, transparent 1.5px)",
        backgroundSize: "24px 24px",
      }}
    >
      <svg className="absolute inset-0 size-full" viewBox="0 0 300 128" fill="none">
        <path
          d="M150 50 C 122 68, 100 76, 78 88"
          stroke="var(--border)"
          strokeWidth="1.5"
          strokeDasharray="4 4"
        />
        <path
          d="M150 50 C 178 68, 200 76, 222 88"
          stroke="var(--border)"
          strokeWidth="1.5"
          strokeDasharray="4 4"
        />
      </svg>
      <div className="absolute top-5 left-1/2 -translate-x-1/2 rounded-[9px] bg-brand px-2.5 py-1.5 text-[11.5px] font-semibold text-brand-foreground ring-2 ring-brand/45">
        {goalLabel}
      </div>
      <div className="absolute top-[72px] left-9 rounded-lg border border-dashed border-input bg-card/90 px-2 py-1 text-[10.5px] text-muted-foreground">
        {probeLabel}
      </div>
      <div className="absolute top-[72px] right-9 rounded-lg border border-dashed border-input bg-card/90 px-2 py-1 text-[10.5px] text-muted-foreground">
        {sourceLabel}
      </div>
    </div>
  );
}

/**
 * LRM-979 — designed empty when the session graph has no nodes (not a blank gray pit).
 * Offers home + create; does not touch LRM-978 boundary chrome.
 */
export function ResearchCanvasEmptyState() {
  const { t } = useT("research");
  const paths = useWorkspacePaths();
  const nav = useNavigation();
  const wsId = useWorkspaceId();
  const qc = useQueryClient();

  const create = useMutation({
    mutationFn: () =>
      api.createResearchSession({
        goal: t(($) => $.session_page.canvas_empty_create_goal),
      }),
    onSuccess: (res) => {
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

  return (
    <div
      data-testid="research-session-canvas-empty"
      className="absolute inset-0 z-[5] flex items-center justify-center overflow-y-auto bg-canvas-bg/92 px-4 py-8 backdrop-blur-[1px]"
    >
      <section className="flex w-full max-w-md flex-col items-center text-center">
        <EmptyMiniCanvas
          goalLabel={t(($) => $.node.goal)}
          probeLabel={t(($) => $.node.probe)}
          sourceLabel={t(($) => $.logic.lane.source)}
        />
        <h2 className="text-sm font-semibold text-foreground">
          {t(($) => $.session_page.canvas_empty_title)}
        </h2>
        <p className="mt-1.5 max-w-[22rem] text-[12.5px] leading-relaxed text-muted-foreground">
          {t(($) => $.session_page.canvas_empty_body)}
        </p>
        {create.isError ? (
          <div
            id="research-canvas-empty-create-error"
            data-testid="research-canvas-empty-create-error"
            role="alert"
            className="mt-3 w-full max-w-xs rounded-lg border border-destructive/35 bg-destructive/5 px-3 py-2 text-left"
          >
            <p className="text-xs font-medium text-destructive">
              {t(($) => $.session_page.canvas_empty_create_failed)}
            </p>
            <p className="mt-0.5 text-[11px] leading-relaxed text-muted-foreground">
              {t(($) => $.session_page.canvas_empty_create_failed_hint)}
            </p>
          </div>
        ) : null}
        <div className="mt-4 flex w-full max-w-xs flex-col gap-2 sm:flex-row sm:justify-center">
          <Button
            type="button"
            variant="outline"
            className="rounded-full"
            data-testid="research-canvas-empty-home"
            onClick={() => nav.push(paths.research())}
          >
            {t(($) => $.session_page.canvas_empty_home)}
          </Button>
          <Button
            type="button"
            className={cn(
              "rounded-full bg-brand text-brand-foreground hover:bg-brand/90",
              create.isPending && "opacity-50 cursor-not-allowed",
            )}
            data-testid="research-canvas-empty-create"
            aria-describedby={
              create.isError ? "research-canvas-empty-create-error" : undefined
            }
            // LRM-1241 — pending must stay focusable (same root cause as LRM-1213/1236).
            aria-disabled={create.isPending || undefined}
            onClick={() => {
              if (create.isPending) return;
              if (create.isError) create.reset();
              create.mutate();
            }}
          >
            {create.isPending
              ? t(($) => $.session_page.canvas_empty_creating)
              : create.isError
                ? t(($) => $.session_page.canvas_empty_retry)
                : t(($) => $.session_page.canvas_empty_create)}
          </Button>
        </div>
      </section>
    </div>
  );
}
