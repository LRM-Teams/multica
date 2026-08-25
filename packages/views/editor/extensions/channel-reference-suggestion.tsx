"use client";

import type { InfiniteData, QueryClient } from "@tanstack/react-query";
import { getCurrentWsId } from "@multica/core/platform";
import {
  conversationGroupChannels,
  conversationKeys,
  flattenConversationPages,
  type ConversationListResponse,
} from "@multica/core/conversations";
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

// No server search — filter the loaded pages of the unified Conversations
// cache, which is also the Messages sidebar's single source of truth. Mirrors
// createIssueReferenceSuggestion's shape and popup
// (issue-reference-suggestion.tsx) minus its server-search fallback.
export function createChannelReferenceSuggestion(
  qc: QueryClient,
): Omit<SuggestionOptions<MentionItem>, "editor"> {
  const pluginKey = new PluginKey("channelReferenceSuggestion");

  function buildItems(query: string): MentionItem[] {
    const wsId = getCurrentWsId();
    if (!wsId) return [];

    const cached = qc.getQueryData<InfiniteData<ConversationListResponse>>(
      conversationKeys.list(wsId),
    );
    const channels = cached
      ? conversationGroupChannels(flattenConversationPages(cached))
      : [];
    const items: MentionItem[] = [];
    for (const c of channels) {
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
    // Tiptap's `Extension.configure()` DEEP-merges the passed suggestion
    // object with ChannelReferenceExtension's addOptions() default
    // (`{ allow: () => false, ... }`, the permanently-disabled fallback for
    // when this suggestion isn't wired up) — see @tiptap/core's
    // Extendable.configure()/mergeDeep. Omitting `allow` here does NOT fall
    // back to @tiptap/suggestion's own `allow = () => true` default; it
    // falls back to the EXTENSION's disabled default instead, silently
    // keeping the picker dead. Must be explicit, not omitted (found live:
    // the composer's # picker never opened in production — Wren, real-device
    // verification).
    allow: () => true,
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
      // Keep #channel suggestions aligned with the @person picker: both are
      // roster-style composer suggestions, span the composer, and stay above
      // it rather than following the caret vertically.
      anchorToEditorWidth: true,
      anchorToEditorTop: true,
      getProps: (props) => ({
        items: props.items,
        query: props.query,
        command: props.command,
      }),
      onKeyDown: (ref, props) => ref?.onKeyDown(props) ?? false,
    }),
  };
}
