"use client";

import type { QueryClient } from "@tanstack/react-query";
import { getCurrentWsId } from "@multica/core/platform";
import { channelKeys } from "@multica/core/channels/queries";
import type { Channel } from "@multica/core/types";
import type { SuggestionOptions } from "@tiptap/suggestion";
import { PluginKey } from "@tiptap/pm/state";
import {
  MentionList,
  type MentionItem,
  type MentionListRef,
} from "./mention-suggestion";
import { matchesPinyin } from "./pinyin-match";
import { createSuggestionPopupRender } from "./suggestion-popup";

function channelToReference(c: Pick<Channel, "id" | "name" | "description">): MentionItem {
  return {
    id: c.id,
    label: c.name,
    type: "channel",
    description: c.description ?? undefined,
  };
}

function matchesChannel(item: Pick<Channel, "name" | "description">, query: string): boolean {
  const q = query.trim().toLowerCase();
  if (!q) return true;
  return (
    item.name.toLowerCase().includes(q) ||
    matchesPinyin(item.name, q) ||
    (!!item.description && item.description.toLowerCase().includes(q))
  );
}

// No server search — a workspace's channel list is small and already fully
// cached (channelsOptions fetches the whole thing up front, unlike issues
// which are paginated/bucketed), so client-side filtering of the cache is
// sufficient. Mirrors createIssueReferenceSuggestion's shape and popup
// (issue-reference-suggestion.tsx) minus its server-search fallback.
export function createChannelReferenceSuggestion(
  qc: QueryClient,
): Omit<SuggestionOptions<MentionItem>, "editor"> {
  const pluginKey = new PluginKey("channelReferenceSuggestion");

  function buildItems(query: string): MentionItem[] {
    const wsId = getCurrentWsId();
    if (!wsId) return [];

    const cached = qc.getQueryData<Channel[]>(channelKeys.list(wsId)) ?? [];
    const items: MentionItem[] = [];
    for (const c of cached) {
      if (c.kind === "group" && !c.archived_at && matchesChannel(c, query)) {
        items.push(channelToReference(c));
      }
    }
    return items;
  }

  return {
    pluginKey,
    char: "#",
    allowedPrefixes: null,
    items: ({ query }) => buildItems(query),
    command: ({ editor, range, props }) => {
      editor
        .chain()
        .focus()
        .insertContentAt(range, {
          type: "channelReference",
          attrs: {
            id: props.id,
            label: props.label,
          },
        })
        .run();
    },
    render: createSuggestionPopupRender<MentionItem, MentionItem, MentionListRef, {
      items: MentionItem[];
      query: string;
      command: (item: MentionItem) => void;
    }>({
      pluginKey,
      component: MentionList,
      getProps: (props) => ({
        items: props.items,
        query: props.query,
        command: props.command,
      }),
      onKeyDown: (ref, props) => ref?.onKeyDown(props) ?? false,
    }),
  };
}
