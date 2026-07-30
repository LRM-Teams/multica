import type { Channel, ChannelNotifyLevel } from "@multica/core/types";
import { isConversationMuted } from "./conversation-muted";
import { useT } from "../../i18n";

type ChannelsT = ReturnType<typeof useT<"channels">>["t"];

/**
 * Resolve the viewer's effective per-channel notify level.
 * LRM-769 contract: the API always returns `notify_level` as one of the four
 * literals. Until that ships, the only persisted signal is `muted_at` and the
 * frozen migration mapping applies (`muted_at` set → "mentions" — legacy mute
 * delivered @-mentions only, never a true silent).
 */
export function resolveChannelNotifyLevel(channel: Channel): ChannelNotifyLevel {
  return (
    channel.notify_level ?? (isConversationMuted(channel) ? "mentions" : "default")
  );
}

/** Short label for the details row value (e.g. "Default (follow global)"). */
export function channelNotifyLevelLabel(
  t: ChannelsT,
  level: ChannelNotifyLevel,
): string {
  switch (level) {
    case "default":
      return t(($) => $.notify_prefs.opt_default);
    case "all":
      return t(($) => $.notify_prefs.opt_all);
    case "mentions":
      return t(($) => $.notify_prefs.opt_mentions);
    case "muted":
      return t(($) => $.notify_prefs.opt_muted);
  }
}
