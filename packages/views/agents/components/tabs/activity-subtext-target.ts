import {
  parseConversationHandle,
  splitTextWithConversationHandles,
} from "@multica/core/conversations";
import type { ConversationHandleLookup } from "@multica/core/conversations";

export type ActivitySubtextPart =
  | { kind: "text"; value: string }
  | { kind: "handle"; value: string };

/** Split Activity subtext so `target: #channel` / `#channel:shortId` can become links. */
export function parseActivitySubtext(subtext: string): ActivitySubtextPart[] {
  return splitTextWithConversationHandles(subtext);
}

export function parseActivityHandle(
  handle: string,
): { channelName: string; messagePrefix: string | null } | null {
  const parsed = parseConversationHandle(handle);
  if (!parsed || parsed.kind !== "channel") return null;
  return { channelName: parsed.name, messagePrefix: parsed.messagePrefix };
}

export function resolveActivityHandleHref(
  handle: string,
  channels: readonly { id: string; name: string; kind?: string }[],
  channelDetail: (id: string) => string,
  lookup?: ConversationHandleLookup | null,
): string | null {
  const parsed = parseActivityHandle(handle);
  if (!parsed) return null;
  if (parsed.messagePrefix) {
    return lookup?.available === true && lookup.href ? lookup.href : null;
  }
  const channel = channels.find(
    (candidate) => candidate.kind !== "dm" && candidate.name === parsed.channelName,
  );
  return channel ? channelDetail(channel.id) : null;
}
