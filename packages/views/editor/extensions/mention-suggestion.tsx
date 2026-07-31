"use client";

import {
  forwardRef,
  useCallback,
  useEffect,
  useImperativeHandle,
  useMemo,
  useRef,
  useState,
  type ReactNode,
} from "react";
import type { QueryClient } from "@tanstack/react-query";
import { getCurrentWsId } from "@multica/core/platform";
import { flattenIssueBuckets, issueKeys } from "@multica/core/issues/queries";
import { workspaceKeys } from "@multica/core/workspace/queries";
import { useAuthStore } from "@multica/core/auth";
import { canAssignAgentToIssue } from "@multica/core/permissions";
import { api } from "@multica/core/api";
import { isImeComposing } from "@multica/core/utils";
import type {
  Issue,
  ListIssuesCache,
  MemberWithUser,
  Agent,
} from "@multica/core/types";
import { Hash, ListTodo } from "lucide-react";
import { ActorAvatar } from "../../common/actor-avatar";
import { agentColor } from "../../common/agent-color";
import { StatusIcon } from "../../issues/components/status-icon";
import { ProjectIcon } from "../../projects/components/project-icon";
import { useT } from "../../i18n/use-t";
import { Badge } from "@multica/ui/components/ui/badge";
import type { IssueStatus, ProjectStatus } from "@multica/core/types";
import { PROJECT_STATUS_CONFIG } from "@multica/core/projects/config";
import type { SuggestionOptions } from "@tiptap/suggestion";
import { PluginKey } from "@tiptap/pm/state";
import {
  getRecencyMap,
  recordMentionUsage,
  sortUserItemsByRecency,
} from "./mention-recency";
import {
  actorHandleSearchRank,
  matchesActorIdentitySearch,
  normalizeActorSearchQuery,
  resolveActorDisplayName,
  resolveActorHandle,
  resolveActorIdentityPresentation,
} from "@multica/core/identity";
import { matchesPinyin } from "./pinyin-match";
import { createSuggestionPopupRender } from "./suggestion-popup";

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

export interface MentionItem {
  id: string;
  label: string;
  type: "member" | "agent" | "squad" | "issue" | "project" | "channel" | "all";
  /** Stable handle for actor mentions. Shown only as weak identity help. */
  handle?: string;
  /** Optional grouping hint for injected context items. */
  group?: "current" | "recent" | "search";
  /** Secondary text shown beside the label (e.g. issue title) */
  description?: string;
  /** Secondary row for actor identity, e.g. @backend-engineer. */
  secondaryLabel?: string;
  /** Issue status for StatusIcon rendering */
  status?: IssueStatus;
  /** Project emoji/icon snapshot for ProjectIcon rendering */
  icon?: string | null;
  /** Project status snapshot for recent/current project rendering */
  projectStatus?: ProjectStatus;
}

interface MentionListProps {
  items: MentionItem[];
  query: string;
  command: (item: MentionItem) => void;
  searchIssues?: (
    query: string,
    signal: AbortSignal,
  ) => Promise<{ issues: Array<Pick<Issue, "id" | "identifier" | "title" | "status">> }>;
  includeProjectSearch?: boolean;
}

export interface MentionListRef {
  onKeyDown: (props: { event: KeyboardEvent }) => boolean;
}

// ---------------------------------------------------------------------------
// Group items by section
// ---------------------------------------------------------------------------

interface MentionGroup {
  label: "Broadcast" | "Current" | "Recent" | "Search" | "Members" | "Issues" | "Channels";
  items: MentionItem[];
}

function groupItems(items: MentionItem[]): MentionGroup[] {
  const broadcast: MentionItem[] = [];
  const current: MentionItem[] = [];
  const recent: MentionItem[] = [];
  const search: MentionItem[] = [];
  const members: MentionItem[] = [];
  const issues: MentionItem[] = [];
  const channels: MentionItem[] = [];

  for (const item of items) {
    if (item.type === "all") {
      broadcast.push(item);
    } else if (item.group === "current") {
      current.push(item);
    } else if (item.group === "recent") {
      recent.push(item);
    } else if (item.group === "search") {
      search.push(item);
    } else if (item.type === "issue" || item.type === "project") {
      issues.push(item);
    } else if (item.type === "channel") {
      channels.push(item);
    } else {
      members.push(item);
    }
  }

  const groups: MentionGroup[] = [];
  if (broadcast.length > 0) groups.push({ label: "Broadcast", items: broadcast });
  if (current.length > 0) groups.push({ label: "Current", items: current });
  if (recent.length > 0) groups.push({ label: "Recent", items: recent });
  if (search.length > 0) groups.push({ label: "Search", items: search });
  if (members.length > 0) groups.push({ label: "Members", items: members });
  if (issues.length > 0) groups.push({ label: "Issues", items: issues });
  if (channels.length > 0) groups.push({ label: "Channels", items: channels });
  return groups;
}

// ---------------------------------------------------------------------------
// MentionList — the popup rendered inside the editor
// ---------------------------------------------------------------------------

const MAX_ITEMS = 20;
const SERVER_CONTEXT_SEARCH_LIMIT = 8;
const SERVER_SEARCH_DEBOUNCE_MS = 150;

const identitySearchOptions = { extendedMatch: matchesPinyin };

function sortActorItems(
  items: MentionItem[],
  recency: ReturnType<typeof getRecencyMap>,
  query: string,
): MentionItem[] {
  return sortUserItemsByRecency(items, recency).toSorted(
    (a, b) =>
      actorHandleSearchRank(a.handle ?? "", query) -
      actorHandleSearchRank(b.handle ?? "", query),
  );
}

function mentionItemKey(item: MentionItem): string {
  return `${item.type}:${item.id}`;
}

// Keep the visible label as the display name. The mention node carries the
// stable target id (`mention://agent/<id>` / `mention://member/<id>`) for routing;
// legacy bare-text fallback is limited to exact handles on the backend.
function mergeMentionItems(
  ...itemGroups: MentionItem[][]
): MentionItem[] {
  const seen = new Set<string>();
  const merged: MentionItem[] = [];

  for (const item of itemGroups.flat()) {
    const key = mentionItemKey(item);
    if (seen.has(key)) continue;
    seen.add(key);
    merged.push(item);
  }

  return merged;
}

export const MentionList = forwardRef<MentionListRef, MentionListProps>(
  function MentionList({ items, query, command, searchIssues, includeProjectSearch = false }, ref) {
    const { t } = useT("editor");
    const [selectedIndex, setSelectedIndex] = useState(0);
    const [serverItems, setServerItems] = useState<MentionItem[]>([]);
    const [isSearching, setIsSearching] = useState(false);
    const [searchedQuery, setSearchedQuery] = useState("");
    const itemRefs = useRef<(HTMLButtonElement | null)[]>([]);
    const normalizedQuery = query.trim();

    useEffect(() => {
      const q = normalizedQuery;
      setServerItems([]);

      if (!q) {
        setIsSearching(false);
        setSearchedQuery("");
        return;
      }

      if (!searchIssues && !includeProjectSearch) {
        setIsSearching(false);
        setSearchedQuery(q);
        return;
      }

      const wsId = getCurrentWsId();
      if (!wsId) {
        setIsSearching(false);
        setSearchedQuery(q);
        return;
      }

      let cancelled = false;
      const controller = new AbortController();
      setIsSearching(true);

      const timer = setTimeout(() => {
        void (async () => {
          try {
            const [issues, projects] = await Promise.all([
              searchIssues
                ? searchIssues(q, controller.signal)
                : api.searchIssues({
                    q,
                    limit: SERVER_CONTEXT_SEARCH_LIMIT,
                    include_closed: true,
                    signal: controller.signal,
                  }),
              includeProjectSearch
                ? api.searchProjects({
                    q,
                    limit: SERVER_CONTEXT_SEARCH_LIMIT,
                    include_closed: true,
                    signal: controller.signal,
                  })
                : Promise.resolve({ projects: [] }),
            ]);
            if (!cancelled && !controller.signal.aborted) {
              setServerItems([
                ...issues.issues.map((issue) => ({ ...issueToMention(issue), group: "search" as const })),
                ...projects.projects.map((project) => ({ ...projectToMention(project), group: "search" as const })),
              ]);
            }
          } catch {
            // Aborted or network error: keep the synchronous cache results.
          } finally {
            if (!cancelled && !controller.signal.aborted) {
              setSearchedQuery(q);
              setIsSearching(false);
            }
          }
        })();
      }, SERVER_SEARCH_DEBOUNCE_MS);

      return () => {
        cancelled = true;
        clearTimeout(timer);
        controller.abort();
      };
    }, [includeProjectSearch, normalizedQuery, searchIssues]);

    const displayItems = useMemo(() => {
      const currentServerItems = searchedQuery === normalizedQuery ? serverItems : [];
      return mergeMentionItems(items, currentServerItems).slice(0, MAX_ITEMS);
    }, [items, normalizedQuery, searchedQuery, serverItems]);

    useEffect(() => {
      setSelectedIndex(0);
    }, [displayItems]);

    useEffect(() => {
      itemRefs.current[selectedIndex]?.scrollIntoView({ block: "nearest" });
    }, [selectedIndex]);

    const selectItem = useCallback(
      (index: number) => {
        const item = displayItems[index];
        if (!item) return;
        const wsId = getCurrentWsId();
        if (wsId) recordMentionUsage(wsId, item);
        command(item);
      },
      [displayItems, command],
    );

    useImperativeHandle(ref, () => ({
      onKeyDown: ({ event }) => {
        // IME is composing — don't intercept Enter/Arrow as picker actions;
        // those keys belong to the IME (Enter commits composition, etc).
        if (isImeComposing(event)) return false;
        if (event.key === "ArrowUp") {
          if (displayItems.length === 0) return true;
          setSelectedIndex(
            (i) => (i + displayItems.length - 1) % displayItems.length,
          );
          return true;
        }
        if (event.key === "ArrowDown") {
          if (displayItems.length === 0) return true;
          setSelectedIndex((i) => (i + 1) % displayItems.length);
          return true;
        }
        if (event.key === "Enter") {
          if (displayItems.length === 0) return true;
          selectItem(selectedIndex);
          return true;
        }
        return false;
      },
    }));

    if (displayItems.length === 0) {
      const isWaitingForServer =
        normalizedQuery !== "" &&
        (isSearching || searchedQuery !== normalizedQuery);

      return (
        <div className="rounded-md border bg-popover p-2 text-xs text-muted-foreground shadow-md">
          {isWaitingForServer
            ? t(($) => $.mention.searching)
            : t(($) => $.mention.no_results)}
        </div>
      );
    }

    const groups = groupItems(displayItems);
    const hasContextGroups = displayItems.some((item) => item.group === "current" || item.group === "recent");
    const contextLayout = hasContextGroups;
    const groupLabel = (label: MentionGroup["label"]): string => {
      if (label === "Broadcast") return "";
      if (label === "Current") return t(($) => $.mention.group_current);
      if (label === "Recent") return t(($) => $.mention.group_recent);
      if (label === "Search") return t(($) => $.mention.group_search);
      if (label === "Members") return t(($) => $.mention.group_members);
      if (label === "Issues") return t(($) => $.mention.group_issues);
      if (label === "Channels") return t(($) => $.mention.group_channels);
      return label;
    };

    // Build a flat index mapping: globalIndex → item
    let globalIndex = 0;
    const duplicateActorLabels = new Set(
      Object.entries(
        displayItems.reduce<Record<string, number>>((acc, item) => {
          if (item.type === "member" || item.type === "agent") {
            const key = item.label.trim().toLowerCase();
            if (key) acc[key] = (acc[key] ?? 0) + 1;
          }
          return acc;
        }, {}),
      )
        .filter(([, count]) => count > 1)
        .map(([label]) => label),
    );

    const renderRows = (group: MentionGroup): ReactNode =>
      group.items.map((item) => {
        const idx = globalIndex++;
        const duplicateLabel = duplicateActorLabels.has(item.label.trim().toLowerCase());
        const showSecondary =
          item.type === "agent" ||
          duplicateLabel ||
          actorHandleSearchRank(item.handle ?? "", normalizedQuery) < 3;
        return (
          <MentionRow
            key={`${item.type}-${item.id}`}
            item={item}
            showSecondary={showSecondary}
            selected={idx === selectedIndex}
            onSelect={() => selectItem(idx)}
            buttonRef={(el) => { itemRefs.current[idx] = el; }}
          />
        );
      });

    if (contextLayout) {
      return (
        <div className="flex max-h-[420px] w-96 flex-col overflow-hidden rounded-lg border bg-popover py-1 shadow-xl">
          {groups.map((group) => {
            const isRecent = group.label === "Recent";
            return (
              <section key={group.label} className={isRecent ? "min-h-0" : "shrink-0"}>
                {group.label !== "Broadcast" && (
                  <div className="shrink-0 px-3 py-2 text-[11px] font-semibold uppercase tracking-wide text-muted-foreground/80">
                    {groupLabel(group.label)}
                  </div>
                )}
                <div className={isRecent ? "max-h-64 overflow-y-auto overscroll-contain" : undefined}>
                  {renderRows(group)}
                </div>
              </section>
            );
          })}
        </div>
      );
    }

    return (
      <div className="w-72 max-h-[300px] overflow-y-auto rounded-md border bg-popover py-1 shadow-md">
        {groups.map((group) => (
          <div key={group.label}>
            {group.label !== "Broadcast" && (
              <div className="px-3 py-2 text-[11px] font-semibold uppercase tracking-wide text-muted-foreground/80">
                {groupLabel(group.label)}
              </div>
            )}
            {renderRows(group)}
          </div>
        ))}
      </div>
    );
  },
);

// ---------------------------------------------------------------------------
// MentionRow — single item in the list
// ---------------------------------------------------------------------------

function MentionRow({
  item,
  showSecondary,
  selected,
  onSelect,
  buttonRef,
}: {
  item: MentionItem;
  showSecondary: boolean;
  selected: boolean;
  onSelect: () => void;
  buttonRef: (el: HTMLButtonElement | null) => void;
}) {
  const { t } = useT("editor");
  if (item.type === "channel") {
    return (
      <button
        type="button"
        ref={buttonRef}
        className={`flex w-full items-center gap-2.5 px-3 py-2 text-left text-xs transition-colors ${
          selected ? "bg-accent" : "hover:bg-accent/50"
        }`}
        onClick={onSelect}
      >
        <span className="flex h-7 w-7 shrink-0 items-center justify-center">
          <Hash className="h-3.5 w-3.5 text-muted-foreground" />
        </span>
        <span className="min-w-0 flex-1">
          <span className="block truncate font-medium text-foreground">{item.label}</span>
          {item.description && (
            <span className="block truncate text-muted-foreground">
              {item.description}
            </span>
          )}
        </span>
      </button>
    );
  }

  if (item.type === "issue") {
    // Visually dim closed issues (done/cancelled) so they're distinguishable
    // from active ones in the suggestion list — they're still selectable.
    const isClosed = item.status === "done" || item.status === "cancelled";
    return (
      <button
        type="button"
        ref={buttonRef}
        className={`flex w-full items-center gap-2.5 px-3 py-2 text-left text-xs transition-colors ${
          selected ? "bg-accent" : "hover:bg-accent/50"
        } ${isClosed ? "opacity-60" : ""}`}
        onClick={onSelect}
      >
        <span className="flex h-7 w-7 shrink-0 items-center justify-center">
          {item.status ? (
            <StatusIcon status={item.status} className="h-3.5 w-3.5" />
          ) : (
            <ListTodo className="h-3.5 w-3.5 text-muted-foreground" />
          )}
        </span>
        <span className="min-w-0 flex-1">
          <span className="flex min-w-0 items-center gap-2">
            <span className="shrink-0 font-medium text-muted-foreground">{item.label}</span>
            {item.description && (
              <span
                className={`truncate text-foreground ${isClosed ? "line-through" : ""}`}
              >
                {item.description}
              </span>
            )}
          </span>
        </span>
      </button>
    );
  }

  if (item.type === "project") {
    const projectStatusCfg = item.projectStatus ? PROJECT_STATUS_CONFIG[item.projectStatus] : null;
    return (
      <button
        type="button"
        ref={buttonRef}
        className={`flex w-full items-center gap-2.5 px-3 py-2 text-left text-xs transition-colors ${
          selected ? "bg-accent" : "hover:bg-accent/50"
        }`}
        onClick={onSelect}
      >
        <span className="flex h-7 w-7 shrink-0 items-center justify-center">
          <ProjectIcon project={{ icon: item.icon ?? null }} size="sm" />
        </span>
        <span className="min-w-0 flex-1">
          <span className="block truncate font-medium text-foreground">{item.label}</span>
          {item.description && (
            <span className="block truncate text-muted-foreground">
              {item.description}
            </span>
          )}
        </span>
        {projectStatusCfg && (
          <span className={`${projectStatusCfg.dotColor} ml-auto size-1.5 shrink-0 rounded-full`} />
        )}
      </button>
    );
  }

  const isActor = item.type === "member" || item.type === "agent";
  const secondary = isActor && showSecondary ? item.secondaryLabel : undefined;
  const allMembersHint = item.type === "all" ? t(($) => $.mention.all_members_hint) : null;
  const actorContent = (
    <>
      <ActorAvatar
        actorType={item.type === "all" ? "member" : item.type}
        actorId={item.id}
        size={20}
        showStatusDot
      />
      <span className="min-w-0 flex-1">
        <span
          className="block truncate font-medium text-foreground"
          style={item.type === "agent" ? { color: agentColor(item.id).fg } : undefined}
        >
          {item.type === "all" ? t(($) => $.mention.all_members) : item.label}
        </span>
        {allMembersHint && (
          <span className="block truncate text-[11px] text-muted-foreground">
            {allMembersHint}
          </span>
        )}
        {secondary && (
          <span className="block truncate text-[11px] text-muted-foreground">
            {secondary}
          </span>
        )}
      </span>
    </>
  );

  return (
    <button
      type="button"
      ref={buttonRef}
      className={`flex w-full items-center gap-2.5 px-3 py-2 text-left text-xs transition-colors ${
        selected ? "bg-accent" : "hover:bg-accent/50"
      }`}
      onClick={onSelect}
    >
      {actorContent}
      {item.type === "agent" && (
        // "Agent" is a glossary-protected product term — kept un-translated.
        // eslint-disable-next-line i18next/no-literal-string
        <Badge variant="outline" className="ml-auto text-[10px] h-4 px-1.5">Agent</Badge>
      )}
    </button>
  );
}

// ---------------------------------------------------------------------------
// Suggestion config factory
// ---------------------------------------------------------------------------

function issueToMention(i: Pick<Issue, "id" | "identifier" | "title" | "status">): MentionItem {
  return {
    id: i.id,
    label: i.identifier,
    type: "issue" as const,
    description: i.title,
    status: i.status as IssueStatus,
  };
}

function projectToMention(p: { id: string; title: string; description?: string | null; icon?: string | null; status?: ProjectStatus }): MentionItem {
  return {
    id: p.id,
    label: p.title,
    type: "project" as const,
    description: p.description ?? undefined,
    icon: p.icon ?? null,
    projectStatus: p.status,
  };
}

function matchesMentionQuery(item: MentionItem, query: string): boolean {
  const q = query.trim().toLowerCase();
  if (!q) return true;
  if (item.type === "member" || item.type === "agent") {
    return matchesActorIdentitySearch(
      item.label,
      item.handle ?? "",
      query,
      identitySearchOptions,
    );
  }
  return (
    item.label.toLowerCase().includes(q) ||
    item.description?.toLowerCase().includes(q) === true ||
    matchesPinyin(item.label, q) ||
    (item.description ? matchesPinyin(item.description, q) : false)
  );
}

/** Lightweight agent shape for channel-scoped @ candidates.
 *  Carries just enough identity (id, name, display_name) for the picker and
 *  the resulting mention chip. Used to surface channel-member agents that
 *  aren't in the member's personal agent list (e.g. a teammate's private
 *  Wendy) — channel membership authorizes the mention, not assignability. */
export interface MentionAgentCandidate {
  id: string;
  name: string;
  display_name?: string | null;
  archived_at?: string | null;
}

interface MentionSuggestionOptions {
  mode?: "default" | "context";
  getContextItems?: () => MentionItem[];
  /** When it returns a set, member/agent/squad candidates are restricted to
   *  those actor ids (e.g. a channel's members) and issues / @all are omitted.
   *  Returns null/undefined to fall back to the full workspace. */
  getAllowedActorIds?: () => ReadonlySet<string> | null | undefined;
  /** Channel-scoped agent candidates (e.g. a channel's agent members) merged
   *  into the @ picker when `getAllowedActorIds` is active. Lets a member @mention
   *  a channel co-member agent they couldn't assign (e.g. a teammate's private
   *  Wendy). Ignored outside channel scope. */
  getScopedAgents?: () => readonly MentionAgentCandidate[] | null | undefined;
}

export function createMentionSuggestion(
  qc: QueryClient,
  options: MentionSuggestionOptions = {},
): Omit<
  SuggestionOptions<MentionItem>,
  "editor"
> {
  // The explicit key is passed into Tiptap Suggestion and reused by the
  // shared popup controller when it dispatches exitSuggestion(view, pluginKey).
  const pluginKey = new PluginKey("mentionSuggestion");

  function buildSyncItems(query: string): MentionItem[] {
    // Read workspace id imperatively because this runs in TipTap factory scope
    // (outside React render). getCurrentWsId() is the non-React singleton set
    // by the URL-driven workspace layout.
    const wsId = getCurrentWsId();
    if (!wsId) return [];

    const members: MemberWithUser[] = qc.getQueryData(workspaceKeys.members(wsId)) ?? [];
    const agents: Agent[] = qc.getQueryData(workspaceKeys.agents(wsId)) ?? [];
    const listQueries = qc.getQueriesData<ListIssuesCache>({ queryKey: issueKeys.list(wsId) });
    const cachedResponse = listQueries[0]?.[1];
    const cachedIssues: Issue[] = cachedResponse ? flattenIssueBuckets(cachedResponse) : [];

    // Read current user identity imperatively — this factory runs outside
    // React render so we can't useAuthStore() as a hook here. The Proxy in
    // packages/core/auth/index.ts forwards `.getState()` to the registered
    // store. Used to gate personal agents in the @mention list so members
    // don't see (or auto-complete) agents they couldn't assign anyway.
    const userId = useAuthStore.getState().user?.id ?? null;
    const myRole =
      members.find((m) => m.user_id === userId)?.role ?? null;

    const q = normalizeActorSearchQuery(query);
    // When set (e.g. a channel's members), candidates are scoped to these ids.
    const allow = options.getAllowedActorIds?.();

    // @all is no longer offered: the bare-mention cutover (#600/#446) dropped
    // the broadcast token — the server neither parses nor triggers `@all`, so
    // surfacing it would be a silent no-op. Notifying everyone is done by
    // @-ing the specific members/agents (Frank's product call).

    const memberItems: MentionItem[] = members
      .filter(
        (m) =>
          matchesActorIdentitySearch(
            resolveActorDisplayName(m, m.name),
            resolveActorHandle(m),
            query,
            identitySearchOptions,
          ) &&
          (!allow || allow.has(m.user_id)),
      )
      .map((m) => {
        const presentation = resolveActorIdentityPresentation(m, m.name);
        return {
          id: m.user_id,
          label: presentation.displayName,
          handle: presentation.handle,
          secondaryLabel: presentation.handleLabel ?? undefined,
          type: "member" as const,
        };
      });

    const agentItems: MentionItem[] = (() => {
      // Channel scope: membership authorizes the mention, not assignability.
      // Inject scoped channel-member agents (e.g. a teammate's private Wendy)
      // that aren't in the member's personal agent list, and skip the
      // assignability gate so co-members are @mentionable.
      const channelScoped = !!allow;
      const scoped = channelScoped ? (options.getScopedAgents?.() ?? null) : null;
      const seen = new Set<string>();
      const candidates: Array<Agent | MentionAgentCandidate> = [];
      for (const a of agents) {
        if (!seen.has(a.id)) {
          seen.add(a.id);
          candidates.push(a);
        }
      }
      if (scoped) {
        for (const a of scoped) {
          if (a.id && !seen.has(a.id)) {
            seen.add(a.id);
            candidates.push(a);
          }
        }
      }
      const items: MentionItem[] = [];
      for (const a of candidates) {
        if (
          a.archived_at ||
          !matchesActorIdentitySearch(
            resolveActorDisplayName(a, a.name),
            resolveActorHandle(a),
            query,
            identitySearchOptions,
          ) ||
          (allow && !allow.has(a.id)) ||
          (!channelScoped &&
            !canAssignAgentToIssue(a as Agent, { userId, role: myRole }).allowed)
        ) {
          continue;
        }
        const presentation = resolveActorIdentityPresentation(a, a.name);
        items.push({
          id: a.id,
          label: presentation.displayName,
          handle: presentation.handle,
          secondaryLabel: presentation.handleLabel ?? undefined,
          type: "agent" as const,
        });
      }
      return items;
    })();

    // Squads are no longer offered in the composer @ picker: the bare-mention
    // cutover (#600/#446) has no server-side squad parse contract, so a picked
    // squad would serialize to bare `@name` the server never resolves into a
    // structured/routing ref — a silent no-op (Barry's #605 gate). Restore when
    // a BE squad bare-token contract exists.

    // Members and agents share a single ranked list — recently mentioned
    // targets come first regardless of type, with an alphabetical fallback
    // for everyone the user hasn't mentioned yet on this device.
    const recency = getRecencyMap(wsId);
    const userItems = sortActorItems(
      [...memberItems, ...agentItems],
      recency,
      query,
    );

    // Cached issues give an instant first paint; MentionList adds server
    // matches for done/cancelled and any other issues not in this cache.
    const issueItems: MentionItem[] = options.mode !== "context" || allow
      ? []
      : cachedIssues
          .filter(
            (i) =>
              i.identifier.toLowerCase().includes(q) ||
              i.title.toLowerCase().includes(q),
          )
          .map(issueToMention);

    return [...userItems, ...issueItems];
  }

  return {
    pluginKey,
    // Trigger the picker even when "@" directly follows another character
    // (e.g. "hi@alice"). Tiptap's Suggestion defaults allowedPrefixes to
    // [" "], which requires a space or node start before the trigger and made
    // mid-word "@" type as plain text. null disables the prefix check, matching
    // chat-composer expectations (Slack / Linear trigger "@" anywhere).
    allowedPrefixes: null,
    items: ({ query }) => {
      if (options.mode === "context") {
        const normalizedQuery = query.trim();
        const contextItems = (options.getContextItems?.() ?? []).filter((item) => matchesMentionQuery(item, query));
        if (!normalizedQuery) return contextItems;
        return mergeMentionItems(contextItems, buildSyncItems(query));
      }
      return buildSyncItems(query);
    },

    render: createSuggestionPopupRender<MentionItem, MentionItem, MentionListRef, MentionListProps>({
      pluginKey,
      component: MentionList,
      getProps: (props) => ({
        items: props.items,
        query: props.query,
        command: props.command,
        // `@` summons a person — it never searches issues/projects (task #57;
        // Parker's rule). Issue/project references go through the `#` picker.
      }),
      onKeyDown: (ref, props) => ref?.onKeyDown(props) ?? false,
    }),
  };
}
