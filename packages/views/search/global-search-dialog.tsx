"use client";

import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Command as CommandPrimitive } from "cmdk";
import { useQuery } from "@tanstack/react-query";
import { SearchIcon, X, ArrowLeft, RotateCw, Hash, User } from "lucide-react";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogDescription,
} from "@multica/ui/components/ui/dialog";
import { cn } from "@multica/ui/lib/utils";
import { useWorkspacePaths } from "@multica/core/paths";
import { useWorkspaceId } from "@multica/core";
import { workspaceSearchOptions, channelAuthorMessageSearchOptions } from "@multica/core/search/queries";
import type {
  WorkspaceSearchChannel,
  WorkspaceSearchDM,
  WorkspaceSearchMessage,
  WorkspaceSearchPerson,
  WorkspaceSearchResponse,
  WorkspaceSearchScope,
  ChannelMessageSearchResult,
} from "@multica/core/types";
import { memberListOptions, agentListOptions } from "@multica/core/workspace/queries";
import { resolveActorDisplayName, resolveActorHandle } from "@multica/core/identity";
import { useNavigation } from "../navigation";
import { useOpenDM } from "../common/use-open-dm";
import { ActorAvatar } from "../common/actor-avatar";
import { useT } from "../i18n";
import { HighlightText } from "./highlight-text";
import { useGlobalSearchStore } from "./global-search-store";
import {
  deriveGlobalSearchStatus,
  scopeCount,
  type GlobalSearchStatus,
} from "./global-search-status";

const SCOPES: WorkspaceSearchScope[] = ["all", "messages", "channels", "dms", "people"];

function useDebounced<T>(value: T, delay: number): T {
  const [debounced, setDebounced] = useState(value);
  useEffect(() => {
    const id = setTimeout(() => setDebounced(value), delay);
    return () => clearTimeout(id);
  }, [value, delay]);
  return debounced;
}

function relativeTime(iso: string): string {
  if (!iso) return "";
  const then = new Date(iso).getTime();
  if (!Number.isFinite(then)) return "";
  const diff = Date.now() - then;
  const min = 60_000;
  const hour = 60 * min;
  const day = 24 * hour;
  if (diff < min) return "now";
  if (diff < hour) return `${Math.floor(diff / min)}m`;
  if (diff < day) return `${Math.floor(diff / hour)}h`;
  if (diff < 7 * day) return `${Math.floor(diff / day)}d`;
  return new Date(iso).toLocaleDateString();
}

export function GlobalSearchDialog() {
  const { t } = useT("search");
  const open = useGlobalSearchStore((s) => s.open);
  const setOpen = useGlobalSearchStore((s) => s.setOpen);
  const scope = useGlobalSearchStore((s) => s.scope);
  const setScope = useGlobalSearchStore((s) => s.setScope);
  const fromAuthor = useGlobalSearchStore((s) => s.fromAuthor);
  const setFromAuthor = useGlobalSearchStore((s) => s.setFromAuthor);
  const includeThread = useGlobalSearchStore((s) => s.includeThread);
  const setIncludeThread = useGlobalSearchStore((s) => s.setIncludeThread);
  const channelId = useGlobalSearchStore((s) => s.channelId);
  const messageRange = useGlobalSearchStore((s) => s.messageRange);
  const setMessageRange = useGlobalSearchStore((s) => s.setMessageRange);
  const setChannelId = useGlobalSearchStore((s) => s.setChannelId);
  const recordRecent = useGlobalSearchStore((s) => s.recordRecent);
  const forgetRecent = useGlobalSearchStore((s) => s.forgetRecent);
  const recentByWorkspace = useGlobalSearchStore((s) => s.recentByWorkspace);

  const wsId = useWorkspaceId();
  const p = useWorkspacePaths();
  const { push } = useNavigation();
  const { openDM } = useOpenDM();

  const [query, setQuery] = useState("");
  const debouncedQuery = useDebounced(query, 250);
  const inputRef = useRef<HTMLInputElement | null>(null);

  // When opened from a channel route, bind 本频道 scope (LRM-873).
  useEffect(() => {
    if (!open || typeof window === "undefined") return;
    const match = window.location.pathname.match(/\/channels\/([^/?#]+)/);
    if (match?.[1]) {
      setChannelId(decodeURIComponent(match[1]));
    }
  }, [open, setChannelId]);

  const recent = recentByWorkspace[wsId] ?? [];

  const { data: members = [] } = useQuery({
    ...memberListOptions(wsId),
    enabled: open,
  });
  const { data: agents = [] } = useQuery({
    ...agentListOptions(wsId),
    enabled: open,
  });

  // Parse leading `from:@handle` / `/from @handle` into a chip on input
  // (LRM-873) — runs in the change handler, not a query→setQuery effect.
  const applyQueryInput = useCallback(
    (raw: string) => {
      const match = raw.match(/^(?:\/from\s+|from:)\s*@?([^\s]+)\s*(.*)$/i);
      if (!match) {
        setQuery(raw);
        return;
      }
      const handle = match[1]!.toLowerCase();
      const rest = match[2] ?? "";
      const member = members.find((m) => {
        const h = resolveActorHandle(m, "").toLowerCase();
        const d = resolveActorDisplayName(m, "").toLowerCase();
        return h === handle || d === handle;
      });
      if (member?.user_id) {
        const label =
          resolveActorHandle(member, "") ||
          resolveActorDisplayName(member, member.user_id);
        setFromAuthor({
          author_type: "user",
          author_id: member.user_id,
          label: label.startsWith("@") ? label : `@${label}`,
        });
        setQuery(rest);
        return;
      }
      const agent = agents.find((a) => {
        const h = resolveActorHandle(a, "").toLowerCase();
        const d = resolveActorDisplayName(a, "").toLowerCase();
        return h === handle || d === handle;
      });
      if (agent) {
        const label = resolveActorDisplayName(agent, agent.name || agent.id);
        setFromAuthor({
          author_type: "agent",
          author_id: agent.id,
          label: label.startsWith("@") ? label : `@${label}`,
        });
        setQuery(rest);
        return;
      }
      setQuery(raw);
    },
    [members, agents, setFromAuthor],
  );

  const useChannelSearch =
    !!fromAuthor &&
    messageRange === "channel" &&
    !!channelId &&
    scope === "messages";

  const {
    data: workspaceSearchData,
    isFetching: workspaceIsFetching,
    isLoading: workspaceIsLoading,
    isError: workspaceIsError,
    refetch: refetchWorkspaceSearch,
  } = useQuery({
    ...workspaceSearchOptions(wsId, debouncedQuery, fromAuthor ? "messages" : scope, {
      limit: 20,
      author_type: fromAuthor?.author_type,
      author_id: fromAuthor?.author_id,
      include_thread: includeThread,
      enabled: open && !useChannelSearch,
    }),
  });

  const {
    data: channelSearchData,
    isFetching: channelIsFetching,
    isLoading: channelIsLoading,
    isError: channelIsError,
    refetch: refetchChannelSearch,
  } = useQuery({
    ...channelAuthorMessageSearchOptions(channelId ?? "", {
      q: debouncedQuery || undefined,
      author_type: fromAuthor?.author_type,
      author_id: fromAuthor?.author_id,
      include_thread: includeThread,
      limit: 20,
    }),
    enabled: open && useChannelSearch,
  });

  const isFetching = useChannelSearch ? channelIsFetching : workspaceIsFetching;
  const isLoading = useChannelSearch ? channelIsLoading : workspaceIsLoading;
  const isError = useChannelSearch ? channelIsError : workspaceIsError;
  const refetch = useChannelSearch ? refetchChannelSearch : refetchWorkspaceSearch;

  const data: WorkspaceSearchResponse | undefined = useMemo(() => {
    if (useChannelSearch) {
      const page = channelSearchData;
      if (!page) return undefined;
      return {
        query: page.query || debouncedQuery,
        scope: "messages",
        counts: {
          messages: page.total,
          channels: 0,
          dms: 0,
          people: 0,
        },
        messages: page.results.map(
          (r: ChannelMessageSearchResult): WorkspaceSearchMessage => ({
            result_type: "message",
            message_id: r.message_id,
            channel_id: r.channel_id,
            channel_name: "",
            channel_kind: "group",
            thread_root_message_id: r.thread_root_message_id,
            hit_count: 1,
            author_type: r.type === "user" || r.type === "agent" ? r.type : null,
            author_id: r.author_id,
            author_name: r.author_name,
            content: r.content,
            snippet: r.content,
            created_at: r.created_at,
          }),
        ),
        channels: [],
        dms: [],
        people: [],
      };
    }
    return workspaceSearchData;
  }, [useChannelSearch, channelSearchData, workspaceSearchData, debouncedQuery]);

  const status: GlobalSearchStatus = useMemo(
    () =>
      deriveGlobalSearchStatus({
        query: fromAuthor ? debouncedQuery || fromAuthor.label : debouncedQuery,
        isFetching,
        isLoading,
        isError,
        data,
      }),
    [debouncedQuery, fromAuthor, isFetching, isLoading, isError, data],
  );

  const scopeLabel = useMemo<Record<WorkspaceSearchScope, string>>(
    () => ({
      all: t(($) => $.globalSearch.scope.all),
      messages: t(($) => $.globalSearch.scope.messages),
      channels: t(($) => $.globalSearch.scope.channels),
      dms: t(($) => $.globalSearch.scope.dms),
      people: t(($) => $.globalSearch.scope.people),
    }),
    [t],
  );

  // Entry points (LRM-454 Lock A / AC#1):
  //   - Header Search button (sidebar) opens this dialog via the store.
  //   - Global ⌘K / Ctrl-K toggles it (LRM-606 reclaim): the legacy
  //     SearchCommand palette (issue/project/nav/member/agent search) has been
  //     deleted, so this is now the single ⌘K owner — no double-trigger risk.
  //     BE contract LRM-605 (`GET /api/search`, SearchGlobal) is live, so the
  //     endpoint no longer 404s.

  // Global ⌘K / Ctrl-K toggles the dialog (mirrors the legacy SearchCommand
  // shortcut, now retired). preventDefault stops the browser's native ⌘K
  // (e.g. Chrome address-bar search).
  useEffect(() => {
    const onKeyDown = (e: KeyboardEvent) => {
      if (e.key === "k" && (e.metaKey || e.ctrlKey)) {
        e.preventDefault();
        useGlobalSearchStore.getState().toggle();
      }
    };
    document.addEventListener("keydown", onKeyDown);
    return () => document.removeEventListener("keydown", onKeyDown);
  }, []);

  // Esc closes (capture phase, before base-ui Dialog).
  useEffect(() => {
    if (!open) return;
    const onEsc = (e: KeyboardEvent) => {
      if (e.key === "Escape") {
        e.preventDefault();
        e.stopPropagation();
        setOpen(false);
      }
    };
    document.addEventListener("keydown", onEsc, true);
    return () => document.removeEventListener("keydown", onEsc, true);
  }, [open, setOpen]);

  // Reset on close; focus input on open.
  useEffect(() => {
    if (!open) {
      setQuery("");
      return;
    }
    const id = setTimeout(() => inputRef.current?.focus(), 0);
    return () => clearTimeout(id);
  }, [open]);

  const closeAndReset = useCallback(() => {
    setOpen(false);
    setQuery("");
  }, [setOpen]);

  const commitSearch = useCallback(
    (q: string) => {
      const trimmed = q.trim();
      if (trimmed) recordRecent(wsId, trimmed);
    },
    [recordRecent, wsId],
  );

  const goto = useCallback(
    (path: string) => {
      commitSearch(query);
      closeAndReset();
      push(path);
    },
    [commitSearch, query, closeAndReset, push],
  );

  const openMessage = useCallback(
    (m: WorkspaceSearchMessage) => {
      const params = new URLSearchParams({ message: m.message_id });
      if (m.thread_root_message_id) params.set("thread", m.thread_root_message_id);
      goto(`${p.channelDetail(m.channel_id)}?${params.toString()}`);
    },
    [goto, p],
  );

  const openChannel = useCallback((c: WorkspaceSearchChannel) => goto(p.channelDetail(c.channel_id)), [goto, p]);
  const openDm = useCallback((d: WorkspaceSearchDM) => goto(p.channelDetail(d.channel_id)), [goto, p]);
  const openPerson = useCallback(
    (person: WorkspaceSearchPerson) => {
      goto(person.actor_type === "agent" ? p.agentDetail(person.actor_id) : p.memberDetail(person.actor_id));
    },
    [goto, p],
  );

  const startDm = useCallback(
    (peerType: "user" | "agent", peerId: string) => {
      commitSearch(query);
      setOpen(false);
      void openDM({ peer_type: peerType, peer_id: peerId });
    },
    [commitSearch, query, setOpen, openDM],
  );

  const handleSubmitRecent = useCallback(
    (q: string) => {
      applyQueryInput(q);
      inputRef.current?.focus();
    },
    [applyQueryInput],
  );
  const countsFor = (s: WorkspaceSearchScope) => (data ? scopeCount(data, s) : 0);

  return (
    <Dialog open={open} onOpenChange={(o) => (o ? setOpen(true) : closeAndReset())}>
      <DialogContent
        showCloseButton={false}
        finalFocus={false}
        aria-describedby={undefined}
        className={cn(
          // Mobile: full-screen page. Desktop (sm+): top-anchored floating panel + scrim.
          "fixed inset-0 top-auto grid max-w-none translate-x-0 translate-y-0 gap-0 overflow-hidden rounded-none bg-popover p-0 text-popover-foreground ring-0",
          "sm:inset-x-0 sm:top-[12vh] sm:mx-auto sm:h-fit sm:w-[min(640px,calc(100vw-2rem))] sm:max-w-[640px] sm:left-1/2 sm:-translate-x-1/2 sm:rounded-xl sm:ring-1 sm:ring-foreground/10",
        )}
      >
        <DialogHeader className="sr-only">
          <DialogTitle>{t(($) => $.globalSearch.title)}</DialogTitle>
          <DialogDescription>{t(($) => $.globalSearch.description)}</DialogDescription>
        </DialogHeader>

        <CommandPrimitive shouldFilter={false} className="flex size-full flex-col overflow-hidden" loop>
          {/* Search bar */}
          <div className="flex flex-col gap-2 border-b px-3 py-3 sm:px-4">
            <div className="flex items-center gap-2">
              <button
                type="button"
                aria-label="Back"
                className="grid size-8 shrink-0 place-items-center rounded-lg bg-primary text-primary-foreground sm:hidden"
                onClick={closeAndReset}
              >
                <ArrowLeft className="size-4" />
              </button>
              <SearchIcon className="size-5 shrink-0 text-muted-foreground" />
              {fromAuthor ? (
                <span
                  data-testid="search-from-chip"
                  className="inline-flex max-w-[40%] shrink-0 items-center gap-1 rounded-md bg-brand-soft px-2 py-0.5 text-xs font-semibold text-brand"
                >
                  {t(($) => $.globalSearch.from_chip, { label: fromAuthor.label })}
                  <button
                    type="button"
                    aria-label={t(($) => $.globalSearch.remove_from_filter)}
                    className="rounded p-0.5 hover:bg-brand/10"
                    onClick={() => setFromAuthor(null)}
                  >
                    <X className="size-3" />
                  </button>
                </span>
              ) : null}
              <CommandPrimitive.Input
                ref={inputRef}
                placeholder={
                  fromAuthor
                    ? t(($) => $.globalSearch.from_placeholder)
                    : t(($) => $.globalSearch.placeholder)
                }
                value={query}
                onValueChange={applyQueryInput}
                className="min-w-0 flex-1 bg-transparent text-base outline-none placeholder:text-muted-foreground"
              />
              {query || fromAuthor ? (
                <button
                  type="button"
                  aria-label="Clear"
                  onClick={() => {
                    setQuery("");
                    setFromAuthor(null);
                    inputRef.current?.focus();
                  }}
                  className="grid size-7 shrink-0 place-items-center rounded-md text-muted-foreground hover:bg-accent"
                >
                  <X className="size-4" />
                </button>
              ) : null}
              <kbd className="hidden shrink-0 rounded border border-border bg-muted px-1.5 py-0.5 text-[10px] font-medium text-muted-foreground sm:inline">
                ESC
              </kbd>
            </div>
            {fromAuthor || scope === "messages" ? (
              <div className="flex flex-wrap items-center gap-1.5 pl-7 sm:pl-9">
                {channelId ? (
                  <>
                    <button
                      type="button"
                      onClick={() => setMessageRange("channel")}
                      className={cn(
                        "rounded-md px-2.5 py-1 text-xs font-medium",
                        messageRange === "channel"
                          ? "bg-brand-soft text-brand"
                          : "bg-muted text-muted-foreground",
                      )}
                    >
                      {t(($) => $.globalSearch.range_channel)}
                    </button>
                    <button
                      type="button"
                      onClick={() => setMessageRange("workspace")}
                      className={cn(
                        "rounded-md px-2.5 py-1 text-xs font-medium",
                        messageRange === "workspace"
                          ? "bg-brand-soft text-brand"
                          : "bg-muted text-muted-foreground",
                      )}
                    >
                      {t(($) => $.globalSearch.range_workspace)}
                    </button>
                  </>
                ) : null}
                <button
                  type="button"
                  onClick={() => setIncludeThread(true)}
                  className={cn(
                    "rounded-md px-2.5 py-1 text-xs font-medium",
                    includeThread ? "bg-brand-soft text-brand" : "bg-muted text-muted-foreground",
                  )}
                >
                  {t(($) => $.globalSearch.include_thread)}
                </button>
                <button
                  type="button"
                  onClick={() => setIncludeThread(false)}
                  className={cn(
                    "rounded-md px-2.5 py-1 text-xs font-medium",
                    !includeThread ? "bg-brand-soft text-brand" : "bg-muted text-muted-foreground",
                  )}
                >
                  {t(($) => $.globalSearch.mainline_only)}
                </button>
              </div>
            ) : null}
          </div>

          {/* Scope tabs */}
          <div className="flex items-center gap-1 overflow-x-auto border-b px-2 py-1.5">
            {SCOPES.map((s) => {
              const active = s === scope;
              const n = countsFor(s);
              return (
                <button
                  key={s}
                  type="button"
                  onClick={() => setScope(s)}
                  className={cn(
                    "flex shrink-0 items-center gap-1.5 rounded-md px-3 py-1.5 text-sm transition-colors",
                    active
                      ? "bg-accent font-semibold text-accent-foreground"
                      : "text-muted-foreground hover:bg-accent/60 hover:text-foreground",
                  )}
                >
                  {scopeLabel[s]}
                  {n > 0 ? <span className="text-xs text-muted-foreground">{n}</span> : null}
                </button>
              );
            })}
          </div>

          {/* Body */}
          <CommandPrimitive.List className="max-h-[min(420px,60vh)] overflow-y-auto overflow-x-hidden sm:max-h-[min(440px,56vh)]">
            {status === "loading" && <SearchSkeleton />}
            {status === "error" && <ErrorState onRetry={() => void refetch()} />}
            {status === "empty" && <EmptyState query={debouncedQuery} />}

            {status === "success" && data && (
              <SearchResults
                data={data}
                scope={scope}
                query={debouncedQuery}
                onOpenMessage={openMessage}
                onOpenChannel={openChannel}
                onOpenDm={openDm}
                onOpenPerson={openPerson}
                onStartDm={startDm}
              />
            )}

            {status === "idle" && (
              <IdleState
                recent={recent}
                onPickRecent={handleSubmitRecent}
                onForgetRecent={(q) => forgetRecent(wsId, q)}
                onJump={(path) => {
                  closeAndReset();
                  push(path);
                }}
              />
            )}
          </CommandPrimitive.List>

          {/* Footer keyboard hints (desktop) */}
          <div className="hidden items-center gap-4 border-t px-4 py-2 text-[11px] text-muted-foreground sm:flex">
            <Hint keys={["↑", "↓"]} label={t(($) => $.globalSearch.scope.all)} />
            <Hint keys={["↵"]} label={t(($) => $.globalSearch.actions.open_message)} />
            <Hint keys={["Esc"]} label={t(($) => $.globalSearch.scope.all)} />
          </div>
        </CommandPrimitive>
      </DialogContent>
    </Dialog>
  );
}

/* ---------- Result rows ---------- */

function SearchResults(props: {
  data: WorkspaceSearchResponse;
  scope: WorkspaceSearchScope;
  query: string;
  onOpenMessage: (m: WorkspaceSearchMessage) => void;
  onOpenChannel: (c: WorkspaceSearchChannel) => void;
  onOpenDm: (d: WorkspaceSearchDM) => void;
  onOpenPerson: (p: WorkspaceSearchPerson) => void;
  onStartDm: (peerType: "user" | "agent", peerId: string) => void;
}) {
  const { data, scope } = props;
  const show = (s: WorkspaceSearchScope) => scope === "all" || scope === s;

  return (
    <>
      {show("channels") && data.channels.length > 0 && (
        <Group labelKey="channels">
          {data.channels.map((c) => (
            <ChannelRow key={`ch:${c.channel_id}`} channel={c} query={props.query} onOpen={() => props.onOpenChannel(c)} />
          ))}
        </Group>
      )}

      {show("dms") && data.dms.length > 0 && (
        <Group labelKey="dms">
          {data.dms.map((d) => (
            <DmRow key={`dm:${d.channel_id}`} dm={d} query={props.query} onOpen={() => props.onOpenDm(d)} />
          ))}
        </Group>
      )}

      {show("messages") && data.messages.length > 0 && (
        <Group labelKey="messages">
          {data.messages.map((m) => (
            <MessageRow key={`msg:${m.message_id}`} message={m} query={props.query} onOpen={() => props.onOpenMessage(m)} />
          ))}
        </Group>
      )}

      {show("people") && data.people.length > 0 && (
        <Group labelKey="people">
          {data.people.map((person) => (
            <PersonRow
              key={`p:${person.actor_type}:${person.actor_id}`}
              person={person}
              query={props.query}
              onOpen={() => props.onOpenPerson(person)}
              onStartDm={() => props.onStartDm(person.actor_type === "agent" ? "agent" : "user", person.actor_id)}
            />
          ))}
        </Group>
      )}
    </>
  );
}

function Group({ labelKey, children }: { labelKey: "channels" | "dms" | "messages" | "people"; children: React.ReactNode }) {
  const { t } = useT("search");
  const label =
    labelKey === "channels"
      ? t(($) => $.globalSearch.groups.channels)
      : labelKey === "dms"
        ? t(($) => $.globalSearch.groups.dms)
        : labelKey === "messages"
          ? t(($) => $.globalSearch.groups.messages)
          : t(($) => $.globalSearch.groups.people);
  return (
    <CommandPrimitive.Group className="p-2">
      <div className="px-3 py-1.5 text-[11px] font-semibold uppercase tracking-wide text-muted-foreground">
        {label}
      </div>
      {children}
    </CommandPrimitive.Group>
  );
}

function RowShell({ onSelect, children }: { onSelect: () => void; children: React.ReactNode }) {
  return (
    <CommandPrimitive.Item
      onSelect={onSelect}
      className="flex cursor-default select-none items-center gap-3 rounded-lg px-3 py-2.5 text-sm outline-none data-[disabled=true]:pointer-events-none data-[disabled=true]:opacity-50 data-selected:bg-accent"
    >
      {children}
    </CommandPrimitive.Item>
  );
}

function ChannelRow({ channel, query, onOpen }: { channel: WorkspaceSearchChannel; query: string; onOpen: () => void }) {
  return (
    <RowShell onSelect={onOpen}>
      <span className="grid size-8 shrink-0 place-items-center rounded-md bg-muted text-muted-foreground">
        <Hash className="size-4" />
      </span>
      <div className="min-w-0 flex-1">
        <div className="truncate font-medium">
          <HighlightText text={channel.name} query={query} />
        </div>
        {channel.description ? (
          <div className="truncate text-xs text-muted-foreground">
            <HighlightText text={channel.description} query={query} />
          </div>
        ) : null}
      </div>
    </RowShell>
  );
}

function DmRow({ dm, query, onOpen }: { dm: WorkspaceSearchDM; query: string; onOpen: () => void }) {
  const { t } = useT("search");
  // V0 contract: BE returns DMs in channel shape (channel_id/name/kind) with no
  // peer payload. Render the channel name, falling back to a localized
  // "私信" placeholder when the server returns no DM name.
  const label = dm.name?.trim() || t(($) => $.globalSearch.row.dm_placeholder);
  return (
    <RowShell onSelect={onOpen}>
      <span className="grid size-8 shrink-0 place-items-center rounded-md bg-muted text-muted-foreground">
        <User className="size-4" />
      </span>
      <div className="min-w-0 flex-1">
        <div className="truncate font-medium">
          <HighlightText text={label} query={query} />
        </div>
      </div>
    </RowShell>
  );
}

function MessageRow({ message, query, onOpen }: { message: WorkspaceSearchMessage; query: string; onOpen: () => void }) {
  const { t } = useT("search");
  const inThread = !!message.thread_root_message_id || message.hit_count > 1;
  const actorType =
    message.author_type === "agent"
      ? "agent"
      : message.author_type === "user"
        ? "member"
        : "member";
  return (
    <RowShell onSelect={onOpen}>
      <ActorAvatar
        actorType={actorType}
        actorId={message.author_id ?? ""}
        size={32}
        name={message.author_name}
        profileLink={false}
        className="shrink-0"
      />
      <div className="min-w-0 flex-1">
        <div className="flex items-center gap-2">
          <span className="shrink-0 rounded border border-border px-1 text-[10px] font-semibold text-muted-foreground">
            {inThread
              ? t(($) => $.globalSearch.row.in_thread)
              : t(($) => $.globalSearch.row.mainline)}
          </span>
          {message.hit_count > 1 ? (
            <span className="shrink-0 rounded border border-primary/40 px-1 text-[10px] font-semibold text-primary">
              {t(($) => $.globalSearch.row.thread_hits, { count: message.hit_count })}
            </span>
          ) : null}
          <span className="truncate font-medium">{message.author_name}</span>
          <span className="truncate text-xs text-muted-foreground">
            · {message.channel_kind === "dm" ? "DM" : message.channel_name ? `#${message.channel_name}` : ""}
          </span>
          <span className="ml-auto shrink-0 text-xs text-muted-foreground">{relativeTime(message.created_at)}</span>
        </div>
        <div className="line-clamp-2 text-sm text-muted-foreground">
          <HighlightText text={message.snippet} query={query} />
        </div>
      </div>
    </RowShell>
  );
}

function PersonRow({
  person,
  query,
  onOpen,
  onStartDm,
}: {
  person: WorkspaceSearchPerson;
  query: string;
  onOpen: () => void;
  onStartDm: () => void;
}) {
  const { t } = useT("search");
  const secondary = person.name && person.name !== person.display_name ? person.name : "";
  return (
    <RowShell onSelect={onOpen}>
      <ActorAvatar
        actorType={person.actor_type}
        actorId={person.actor_id}
        size={32}
        avatarUrlHint={person.avatar_url}
        name={person.display_name}
        profileLink={false}
        className="shrink-0"
      />
      <div className="min-w-0 flex-1">
        <div className="truncate font-medium">
          <HighlightText text={person.display_name} query={query} />
        </div>
        {secondary ? (
          <div className="truncate text-xs text-muted-foreground">
            <HighlightText text={secondary} query={query} />
          </div>
        ) : null}
      </div>
      <button
        type="button"
        onPointerDown={(e) => {
          e.preventDefault();
          e.stopPropagation();
        }}
        onClick={(e) => {
          e.preventDefault();
          e.stopPropagation();
          onStartDm();
        }}
        className="shrink-0 rounded-md px-2 py-1 text-xs text-muted-foreground hover:bg-accent hover:text-foreground"
      >
        {t(($) => $.globalSearch.actions.open_dm)}
      </button>
    </RowShell>
  );
}

/* ---------- States ---------- */

function SearchSkeleton() {
  return (
    <div data-testid="global-search-skeleton" className="p-4" aria-busy="true">
      {[40, 88, 70, 82, 60].map((w, i) => (
        <div key={i} className="mb-2 h-3 animate-pulse rounded bg-muted" style={{ width: `${w}%` }} />
      ))}
    </div>
  );
}

function EmptyState({ query }: { query: string }) {
  const { t } = useT("search");
  return (
    <div className="px-5 py-10 text-center text-sm text-muted-foreground">
      {t(($) => $.globalSearch.states.empty, { query: query.trim() })}
    </div>
  );
}

function ErrorState({ onRetry }: { onRetry: () => void }) {
  const { t } = useT("search");
  return (
    <div className="px-5 py-10 text-center text-sm text-muted-foreground">
      <div className="mb-1">{t(($) => $.globalSearch.states.error_title)}</div>
      <button
        type="button"
        onClick={onRetry}
        className="mt-3 inline-flex items-center gap-1.5 rounded-lg border border-border px-3 py-1.5 text-xs font-semibold text-primary hover:bg-accent"
      >
        <RotateCw className="size-3.5" />
        {t(($) => $.globalSearch.states.retry)}
      </button>
    </div>
  );
}

function IdleState(props: {
  recent: string[];
  onPickRecent: (q: string) => void;
  onForgetRecent: (q: string) => void;
  onJump: (path: string) => void;
}) {
  const { recent, onPickRecent, onForgetRecent } = props;
  const { t } = useT("search");
  return (
    <>
      {recent.length > 0 && (
        <CommandPrimitive.Group className="p-2">
          <div className="px-3 py-1.5 text-[11px] font-semibold uppercase tracking-wide text-muted-foreground">
            {t(($) => $.globalSearch.states.recent)}
          </div>
          {recent.map((q) => (
            <CommandPrimitive.Item
              key={q}
              value={`recent:${q}`}
              onSelect={() => onPickRecent(q)}
              className="flex cursor-default select-none items-center gap-2 rounded-lg px-3 py-2 text-sm outline-none data-selected:bg-accent"
            >
              <span className="text-muted-foreground">🕘</span>
              <span className="flex-1 truncate">{q}</span>
              <button
                type="button"
                aria-label="Remove"
                onPointerDown={(e) => {
                  e.preventDefault();
                  e.stopPropagation();
                }}
                onClick={(e) => {
                  e.preventDefault();
                  e.stopPropagation();
                  onForgetRecent(q);
                }}
                className="grid size-6 shrink-0 place-items-center rounded text-muted-foreground hover:bg-accent hover:text-foreground"
              >
                <X className="size-3.5" />
              </button>
            </CommandPrimitive.Item>
          ))}
        </CommandPrimitive.Group>
      )}
      <div className="px-5 py-4 text-center text-xs text-muted-foreground">
        {t(($) => $.globalSearch.placeholder)}
      </div>
    </>
  );
}

function Hint({ keys, label }: { keys: string[]; label: string }) {
  return (
    <span className="flex items-center gap-1.5">
      {keys.map((k) => (
        <kbd key={k} className="rounded border border-border bg-muted px-1 py-0.5 font-mono text-[10px]">
          {k}
        </kbd>
      ))}
      <span>{label}</span>
    </span>
  );
}
