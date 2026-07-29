"use client";

import { PauseCircle, PlayCircle, Plus } from "lucide-react";
import { showErrorToast } from "@multica/ui/lib/error-toast";
import { Button } from "@multica/ui/components/ui/button";
import { cn } from "@multica/ui/lib/utils";
import { useAgentDMControl, useAgentDMGlobalControl } from "@multica/core/dm";
import type { AgentDMControl, AgentDMControlAction } from "@multica/core/dm";
import { useT } from "../../i18n/use-t";

/**
 * How many rounds one "grant more rounds" click adds. Fixed + explicit so the
 * button label ("再放行 N 轮") always matches the `rounds` we actually send —
 * the BE would otherwise apply its own default and the label could disagree.
 */
const GRANT_ROUNDS_STEP = 3;

/**
 * #692 owner control strip for a supervised agent↔agent DM. Sits between the DM
 * header and the message list (the banner slot). Shows the live gate/round
 * state and the owner's read-only-supervisor actions — pause/resume this pair,
 * grant the current exchange more rounds, pause/resume all workspace agent DMs.
 * The available actions come from `control.actions` (BE-authoritative); the
 * `view_dm` nav affordance is dropped here since we are already inside the DM.
 * Only the owner ever receives a populated `a2a_control`, so this renders for
 * nobody else.
 */
export function AgentDMControlStrip({
  channelId,
  control,
}: {
  channelId: string;
  control: AgentDMControl;
}) {
  const { t } = useT("channels");
  const agentDMControl = useAgentDMControl();
  const agentDMGlobalControl = useAgentDMGlobalControl();
  const busy = agentDMControl.isPending || agentDMGlobalControl.isPending;

  const paused = control.state !== "active";
  const stateLabel =
    control.state === "active"
      ? t(($) => $.dm.agent_pair.state_active, {
          round: control.round,
          roundLimit: control.round_limit,
        })
      : control.state === "paused_budget"
        ? t(($) => $.dm.agent_pair.state_paused_budget)
        : control.state === "paused_frequency"
          ? t(($) => $.dm.agent_pair.state_paused_frequency)
          : control.state === "paused_pair"
            ? t(($) => $.dm.agent_pair.state_paused_pair)
            : t(($) => $.dm.agent_pair.state_paused_global);

  // `view_dm` is a client-only nav affordance for the inbox / source group, not
  // an action to re-render inside the DM the owner is already reading.
  const actions = control.actions.filter(
    (action): action is Exclude<AgentDMControlAction, "view_dm"> => action !== "view_dm",
  );

  const run = (action: Exclude<AgentDMControlAction, "view_dm">) => {
    const onError = { onError: () => showErrorToast(t(($) => $.dm.agent_pair.action_failed)) };
    // Global pause/resume is a workspace-level action with its own endpoint +
    // source-of-truth state (Barry's contract) — never derive it from this DM
    // channel. Pair/exchange actions stay on the per-channel control endpoint.
    if (action === "pause_global" || action === "resume_global") {
      agentDMGlobalControl.mutate(action, onError);
      return;
    }
    agentDMControl.mutate(
      {
        channelId,
        action,
        exchangeId: action === "grant_rounds" ? control.exchange_id : undefined,
        rounds: action === "grant_rounds" ? GRANT_ROUNDS_STEP : undefined,
      },
      onError,
    );
  };

  const actionButton = (action: Exclude<AgentDMControlAction, "view_dm">) => {
    switch (action) {
      case "grant_rounds":
        return (
          <Button key={action} variant="outline" size="sm" disabled={busy} onClick={() => run(action)}>
            <Plus className="size-3.5" />
            {t(($) => $.dm.agent_pair.action_grant_rounds, { step: GRANT_ROUNDS_STEP })}
          </Button>
        );
      case "pause_pair":
        return (
          <Button key={action} variant="outline" size="sm" disabled={busy} onClick={() => run(action)}>
            <PauseCircle className="size-3.5" />
            {t(($) => $.dm.agent_pair.action_pause_pair)}
          </Button>
        );
      case "resume_pair":
        return (
          <Button key={action} variant="outline" size="sm" disabled={busy} onClick={() => run(action)}>
            <PlayCircle className="size-3.5" />
            {t(($) => $.dm.agent_pair.action_resume_pair)}
          </Button>
        );
      case "pause_global":
        return (
          <Button key={action} variant="ghost" size="sm" disabled={busy} onClick={() => run(action)}>
            {t(($) => $.dm.agent_pair.action_pause_global)}
          </Button>
        );
      case "resume_global":
        return (
          <Button key={action} variant="ghost" size="sm" disabled={busy} onClick={() => run(action)}>
            {t(($) => $.dm.agent_pair.action_resume_global)}
          </Button>
        );
      default:
        return null;
    }
  };

  return (
    <div
      data-testid="agent-dm-control-strip"
      className={cn(
        "flex flex-wrap items-center gap-x-3 gap-y-1.5 border-b border-border px-4 py-2 text-xs",
        paused ? "bg-muted/40 text-foreground" : "bg-transparent text-muted-foreground",
      )}
    >
      <span className="inline-flex items-center gap-1.5 font-medium">
        <span
          aria-hidden
          className={cn(
            "size-1.5 rounded-full",
            paused ? "bg-muted-foreground" : "bg-emerald-500",
          )}
        />
        {stateLabel}
      </span>
      {actions.length > 0 && (
        <div className="flex flex-wrap items-center gap-1.5">{actions.map(actionButton)}</div>
      )}
    </div>
  );
}
