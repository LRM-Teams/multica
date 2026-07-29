"use client";

import { ExternalLink, PauseCircle, Plus } from "lucide-react";
import { showErrorToast } from "@multica/ui/lib/error-toast";
import { Button } from "@multica/ui/components/ui/button";
import { useWorkspacePaths } from "@multica/core/paths";
import { useAgentDMControl, useAgentDMGlobalControl } from "@multica/core/dm";
import type { AgentDMControlAction } from "@multica/core/dm";
import type { InboxItem } from "@multica/core/types";
import { parseAgentDMPausedInbox } from "./agent-dm-paused-inbox";
import { useNavigation } from "../../navigation";
import { useT } from "../../i18n";

/** Rounds one "grant more rounds" click adds — matches AgentDMControlStrip. */
const GRANT_ROUNDS_STEP = 3;

/**
 * Owner actions on an `agent_dm_paused` inbox alert (FE-6). Mirrors
 * AgentDMControlStrip: `view_dm` navigates to the DM (client-only); the pair
 * actions reuse the per-channel control mutation; `pause_global` uses the
 * workspace-level global mutation. Actions come from the alert payload.
 */
export function AgentDMPausedInboxActions({ item }: { item: InboxItem }) {
  const { t } = useT("channels");
  const navigation = useNavigation();
  const paths = useWorkspacePaths();
  const control = useAgentDMControl();
  const globalControl = useAgentDMGlobalControl();
  const busy = control.isPending || globalControl.isPending;

  // Parse is a pure function (no hooks) — safe to guard after the hooks above so
  // a non-A2A / malformed inbox item simply renders no actions.
  const data = parseAgentDMPausedInbox(item);
  if (!data) return null;

  const onError = { onError: () => showErrorToast(t(($) => $.dm.agent_pair.action_failed)) };

  const actionButton = (action: AgentDMControlAction) => {
    switch (action) {
      case "view_dm":
        return (
          <Button
            key={action}
            variant="outline"
            size="sm"
            onClick={() => navigation.push(paths.channelDetail(data.dmChannelId))}
          >
            <ExternalLink className="size-3.5" />
            {t(($) => $.dm.agent_pair.action_view_dm)}
          </Button>
        );
      case "grant_rounds":
        return (
          <Button
            key={action}
            variant="outline"
            size="sm"
            disabled={busy}
            onClick={() =>
              control.mutate(
                {
                  channelId: data.dmChannelId,
                  action: "grant_rounds",
                  exchangeId: data.exchangeId,
                  rounds: GRANT_ROUNDS_STEP,
                },
                onError,
              )
            }
          >
            <Plus className="size-3.5" />
            {t(($) => $.dm.agent_pair.action_grant_rounds, { step: GRANT_ROUNDS_STEP })}
          </Button>
        );
      case "pause_pair":
        return (
          <Button
            key={action}
            variant="outline"
            size="sm"
            disabled={busy}
            onClick={() =>
              control.mutate({ channelId: data.dmChannelId, action: "pause_pair" }, onError)
            }
          >
            <PauseCircle className="size-3.5" />
            {t(($) => $.dm.agent_pair.action_pause_pair)}
          </Button>
        );
      case "pause_global":
        return (
          <Button
            key={action}
            variant="ghost"
            size="sm"
            disabled={busy}
            onClick={() => globalControl.mutate("pause_global", onError)}
          >
            {t(($) => $.dm.agent_pair.action_pause_global)}
          </Button>
        );
      default:
        return null;
    }
  };

  return <>{data.actions.map(actionButton)}</>;
}
