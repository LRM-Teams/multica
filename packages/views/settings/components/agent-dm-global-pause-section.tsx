"use client";

import { PauseCircle, PlayCircle } from "lucide-react";
import { toast } from "sonner";
import { useQuery } from "@tanstack/react-query";
import { Button } from "@multica/ui/components/ui/button";
import { Card, CardContent } from "@multica/ui/components/ui/card";
import { useAuthStore } from "@multica/core/auth";
import { useWorkspaceId } from "@multica/core/hooks";
import { agentListOptions } from "@multica/core/workspace/queries";
import { agentDMGlobalControlOptions, useAgentDMGlobalControl } from "@multica/core/dm";
import { useT } from "../../i18n";

/**
 * #692 workspace-level "pause all agent↔agent DMs" control (FE-7). Owner-only:
 * the server lets only a user who owns at least one non-archived agent read or
 * write the global control, so the section is gated on local agent ownership —
 * non-owners never fire the (would-be 403) request and never see the section.
 * Copy lives in the `channels` namespace alongside the rest of the A2A chrome.
 */
export function AgentDMGlobalPauseSection() {
  const { t } = useT("channels");
  const userId = useAuthStore((s) => s.user?.id);
  const wsId = useWorkspaceId();
  const { data: agents = [] } = useQuery(agentListOptions(wsId));
  const ownsAnyAgent = !!userId && agents.some((a) => a.owner_id === userId);
  const { data: control } = useQuery({
    ...agentDMGlobalControlOptions(wsId),
    enabled: !!wsId && ownsAnyAgent,
  });
  const globalControl = useAgentDMGlobalControl();

  // Nothing to show until we've confirmed the viewer owns an agent AND read the
  // current global state — no flash of an empty/half section for everyone else.
  if (!ownsAnyAgent || !control) return null;

  const paused = control.paused;
  const toggle = () => {
    globalControl.mutate(paused ? "resume_global" : "pause_global", {
      onError: () => toast.error(t(($) => $.dm.agent_pair.action_failed)),
    });
  };

  return (
    <section className="space-y-4">
      <h2 className="text-sm font-semibold">{t(($) => $.dm.agent_pair.global_section_title)}</h2>
      <Card>
        <CardContent>
          <div className="flex flex-col gap-2 sm:flex-row sm:items-center sm:justify-between">
            <p className="text-xs text-muted-foreground">
              {paused
                ? t(($) => $.dm.agent_pair.global_paused_note)
                : t(($) => $.dm.agent_pair.global_section_desc)}
            </p>
            <Button
              variant="outline"
              size="sm"
              className="shrink-0"
              onClick={toggle}
              disabled={globalControl.isPending || !control.can_pause_global}
            >
              {paused ? (
                <>
                  <PlayCircle className="size-3.5" />
                  {t(($) => $.dm.agent_pair.action_resume_global)}
                </>
              ) : (
                <>
                  <PauseCircle className="size-3.5" />
                  {t(($) => $.dm.agent_pair.global_toggle)}
                </>
              )}
            </Button>
          </div>
        </CardContent>
      </Card>
    </section>
  );
}
