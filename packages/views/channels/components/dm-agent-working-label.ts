import type { AgentPresenceDetail } from "@multica/core/agents";
import type { AgentTask } from "@multica/core/types";
import { toLiveAvailability } from "../../agents/presence";
import type { ActivityEvent } from "../../agents/components/tabs/activity-event";
import { friendlyBubbleToolLabel } from "../../chat/lib/bubble-cursor-activity";

/**
 * Short bubble-style working label for agent DM header.
 * Examples: 思考中 / Edit / Shell — never path/command details or Working list.
 */
export function resolveDmShortWorkingLabel(args: {
  presence: AgentPresenceDetail | "loading" | null | undefined;
  activeTask: AgentTask | null;
  latestActivity: ActivityEvent | null;
  thinkingLabel: string;
  queuedLabel: string;
  startingLabel: string;
}): string | null {
  const { presence, activeTask, latestActivity, thinkingLabel, queuedLabel, startingLabel } =
    args;
  if (!presence || presence === "loading") return null;
  if (!activeTask) return null;

  const live = toLiveAvailability(presence.availability);
  if (live === null || live === "offline") return null;

  if (latestActivity) {
    if (latestActivity.activity_kind === "tool_call") {
      return friendlyBubbleToolLabel(latestActivity.tool);
    }
    if (latestActivity.activity_kind === "thinking") {
      return thinkingLabel;
    }
  }

  if (activeTask.status === "queued") return queuedLabel;
  if (activeTask.status === "dispatched") return startingLabel;
  if (activeTask.status === "waiting_local_directory") return startingLabel;
  return thinkingLabel;
}
