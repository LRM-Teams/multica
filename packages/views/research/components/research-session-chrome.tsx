"use client";

import { Badge } from "@multica/ui/components/ui/badge";
import { Button } from "@multica/ui/components/ui/button";
import { Checkbox } from "@multica/ui/components/ui/checkbox";
import type { ResearchSession } from "@multica/core/types";
import { useT } from "../../i18n/use-t";

export function ResearchSessionChrome({
  session,
  canConfirm,
  canHandoff,
  createProject,
  createChannel,
  onCreateProjectChange,
  onCreateChannelChange,
  onConfirm,
  onHandoff,
  confirmPending,
  handoffPending,
  onOpenDelivery,
  selectedSummary,
}: {
  session: ResearchSession;
  canConfirm: boolean;
  canHandoff: boolean;
  createProject: boolean;
  createChannel: boolean;
  onCreateProjectChange: (v: boolean) => void;
  onCreateChannelChange: (v: boolean) => void;
  onConfirm: () => void;
  onHandoff: () => void;
  confirmPending?: boolean;
  handoffPending?: boolean;
  onOpenDelivery?: () => void;
  selectedSummary?: string | null;
}) {
  const { t } = useT("research");

  return (
    <header className="flex shrink-0 flex-col gap-2 border-b bg-background/80 px-4 py-3 backdrop-blur">
      <div className="flex items-start justify-between gap-3">
        <div className="min-w-0">
          <div className="flex flex-wrap items-center gap-2">
            <h1 className="truncate text-base font-semibold tracking-tight">{session.title}</h1>
            <Badge variant="secondary">
              {t(($) => $.status[session.status as keyof typeof $.status] ?? session.status)}
            </Badge>
            <Badge variant="outline" className="font-mono text-[10px] uppercase">
              {session.current_stage}
            </Badge>
          </div>
          <p className="mt-0.5 truncate text-xs text-muted-foreground">{session.goal}</p>
        </div>
        <div className="flex shrink-0 flex-wrap items-center justify-end gap-2">
          {onOpenDelivery ? (
            <Button size="sm" variant="outline" onClick={onOpenDelivery}>
              {t(($) => $.panel.delivery)}
            </Button>
          ) : null}
          {canConfirm && session.status !== "completed" ? (
            <Button size="sm" onClick={onConfirm} disabled={confirmPending}>
              {t(($) => $.panel.confirm)}
            </Button>
          ) : null}
          {canHandoff ? (
            <>
              <label className="flex items-center gap-1.5 text-[11px] text-muted-foreground">
                <Checkbox
                  checked={createProject}
                  onCheckedChange={(v) => onCreateProjectChange(v === true)}
                />
                {t(($) => $.panel.handoff_project)}
              </label>
              <label className="flex items-center gap-1.5 text-[11px] text-muted-foreground">
                <Checkbox
                  checked={createChannel}
                  onCheckedChange={(v) => onCreateChannelChange(v === true)}
                />
                {t(($) => $.panel.handoff_channel)}
              </label>
              <Button
                size="sm"
                variant="secondary"
                disabled={handoffPending || (!createProject && !createChannel)}
                onClick={onHandoff}
              >
                {t(($) => $.panel.handoff)}
              </Button>
            </>
          ) : null}
        </div>
      </div>
      {selectedSummary ? (
        <p className="line-clamp-2 rounded-md border bg-muted/40 px-3 py-1.5 text-[11px] text-muted-foreground">
          {selectedSummary}
        </p>
      ) : null}
    </header>
  );
}
