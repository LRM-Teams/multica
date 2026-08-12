"use client";

import type { QueryClient } from "@tanstack/react-query";
import { getCurrentWsId } from "@multica/core/platform";
import { flattenIssueBuckets, issueKeys } from "@multica/core/issues/queries";
import { api } from "@multica/core/api";
import type { Issue, ListIssuesCache } from "@multica/core/types";
import type { SuggestionOptions } from "@tiptap/suggestion";
import { PluginKey } from "@tiptap/pm/state";
import {
  MentionList,
  type MentionItem,
  type MentionListRef,
} from "./mention-suggestion";
import { matchesPinyin } from "./pinyin-match";
import { createSuggestionPopupRender } from "./suggestion-popup";

const SERVER_ISSUE_SEARCH_LIMIT = 8;

function issueToReference(i: Pick<Issue, "id" | "identifier" | "title" | "status">): MentionItem {
  return {
    id: i.id,
    label: i.identifier,
    type: "issue",
    description: i.title,
    status: i.status,
  };
}

function matchesIssue(item: Pick<Issue, "identifier" | "title">, query: string): boolean {
  const q = query.trim().toLowerCase();
  if (!q) return true;
  return (
    item.identifier.toLowerCase().includes(q) ||
    item.title.toLowerCase().includes(q) ||
    matchesPinyin(item.identifier, q) ||
    matchesPinyin(item.title, q)
  );
}

/**
 * Notes `#` picker for inserting `issueReference` nodes.
 * Mutually exclusive with channel `#` references — enable only one per editor.
 */
export function createIssueReferenceSuggestion(
  qc: QueryClient,
): Omit<SuggestionOptions<MentionItem>, "editor"> {
  const pluginKey = new PluginKey("issueReferenceSuggestion");

  function buildSyncItems(query: string): MentionItem[] {
    const wsId = getCurrentWsId();
    if (!wsId) return [];

    const listQueries = qc.getQueriesData<ListIssuesCache>({ queryKey: issueKeys.list(wsId) });
    const cachedResponse = listQueries[0]?.[1];
    const cachedIssues: Issue[] = cachedResponse ? flattenIssueBuckets(cachedResponse) : [];

    return cachedIssues.filter((issue) => matchesIssue(issue, query)).map(issueToReference);
  }

  return {
    pluginKey,
    char: "#",
    allowedPrefixes: null,
    // Must be explicit — Extension.configure deep-merges over the extension's
    // disabled `allow: () => false` default (same footgun as channel refs).
    allow: () => true,
    items: ({ query }) => buildSyncItems(query),
    command: ({ editor, range, props }) => {
      editor
        .chain()
        .focus()
        .insertContentAt(range, {
          type: "issueReference",
          attrs: {
            id: props.id,
            // Persist identifier only — never bake title into markdown label so
            // inaccessible chips cannot leak a previously cached title.
            label: props.label,
            title: null,
          },
        })
        .run();
    },
    render: createSuggestionPopupRender<
      MentionItem,
      MentionItem,
      MentionListRef,
      {
        items: MentionItem[];
        query: string;
        command: (item: MentionItem) => void;
        searchIssues?: MentionListPropsSearch;
      }
    >({
      pluginKey,
      component: MentionList,
      getProps: (props) => ({
        items: props.items,
        query: props.query,
        command: props.command,
        searchIssues: (q: string, signal: AbortSignal) =>
          api.searchIssues({
            q,
            limit: SERVER_ISSUE_SEARCH_LIMIT,
            include_closed: true,
            signal,
          }),
      }),
      onKeyDown: (ref, props) => ref?.onKeyDown(props) ?? false,
    }),
  };
}

type MentionListPropsSearch = (
  query: string,
  signal: AbortSignal,
) => Promise<{ issues: Array<Pick<Issue, "id" | "identifier" | "title" | "status">> }>;
