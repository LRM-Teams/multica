import type { NotePage } from "@multica/core/types";

export function effectiveNoteShareIds(pages: readonly NotePage[], page: NotePage) {
  const byId = new Map(pages.map((entry) => [entry.id, entry]));
  byId.set(page.id, page);

  const shareUserIds: string[] = [];
  const shareChannelIds: string[] = [];
  const seenUsers = new Set<string>();
  const seenChannels = new Set<string>();
  const seenPages = new Set<string>();

  let current: NotePage | undefined = page;
  while (current && !seenPages.has(current.id)) {
    seenPages.add(current.id);
    for (const id of current.share_user_ids) {
      if (seenUsers.has(id)) continue;
      seenUsers.add(id);
      shareUserIds.push(id);
    }
    for (const id of current.share_channel_ids ?? []) {
      if (seenChannels.has(id)) continue;
      seenChannels.add(id);
      shareChannelIds.push(id);
    }
    current = current.parent_id ? byId.get(current.parent_id) : undefined;
  }

  return {
    shareUserIds,
    shareAgentIds: [...(page.share_agent_ids ?? [])],
    shareChannelIds,
  };
}
