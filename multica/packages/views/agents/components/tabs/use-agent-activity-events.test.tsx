// @vitest-environment jsdom

import { describe, it, expect, vi, beforeEach } from "vitest";
import { renderHook, act } from "@testing-library/react";
import type { ActivityEvent } from "./activity-event";

// WS event registry — captures the handler the hook registers so tests can
// simulate a server push by invoking it directly.
const wsHandlers = vi.hoisted(() => new Map<string, (payload: unknown) => void>());
// Captures the reconnect callback the hook registers.
const reconnect = vi.hoisted(() => ({ cb: null as null | (() => void) }));
// Controllable stand-in for what useInfiniteQuery returns this render. `pages`
// is one event array per fetched REST page (newest page first, older pages
// appended as the reader scrolls up); the hook flattens them through the
// id-keyed reducer.
const queryState = vi.hoisted(() => ({
  pages: [[]] as ActivityEvent[][],
  isLoading: false,
  isError: false,
  refetch: vi.fn(),
  hasNextPage: false,
  isFetchingNextPage: false,
  fetchNextPage: vi.fn(),
}));
const clientHandles = vi.hoisted(() => ({ invalidateQueries: vi.fn() }));

vi.mock("@multica/core/agents", () => ({
  agentActivityEventsKeys: {
    all: (agentId: string) => ["agent-activity-events", agentId],
  },
  agentActivityEventsOptions: (agentId: string) => ({
    queryKey: ["agent-activity-events", agentId],
    queryFn: () => Promise.resolve({ events: [], has_more: false, next_cursor: null }),
    initialPageParam: null,
    getNextPageParam: () => undefined,
  }),
}));

vi.mock("@tanstack/react-query", async () => {
  const actual = await vi.importActual<typeof import("@tanstack/react-query")>(
    "@tanstack/react-query",
  );
  return {
    ...actual,
    // Wrap the test's `pages` into the InfiniteData shape the hook flattens.
    // Changing `pages` + rerender models a fetch resolving / an older page
    // arriving; the extra flags drive the load-older controls.
    useInfiniteQuery: () => ({
      data: {
        pages: queryState.pages.map((events) => ({ events })),
        pageParams: queryState.pages.map(() => null),
      },
      isLoading: queryState.isLoading,
      isError: queryState.isError,
      refetch: queryState.refetch,
      fetchNextPage: queryState.fetchNextPage,
      hasNextPage: queryState.hasNextPage,
      isFetchingNextPage: queryState.isFetchingNextPage,
    }),
    useQueryClient: () => ({ invalidateQueries: clientHandles.invalidateQueries }),
  };
});

vi.mock("@multica/core/realtime", () => ({
  useWSEvent: (event: string, handler: (payload: unknown) => void) => {
    wsHandlers.set(event, handler);
  },
  useWSReconnect: (cb: () => void) => {
    reconnect.cb = cb;
  },
}));

import { useAgentActivityEvents } from "./use-agent-activity-events";

function evt(id: string, occurred_at: string, over: Partial<ActivityEvent> = {}): ActivityEvent {
  return {
    id,
    agent_id: "agent-1",
    occurred_at,
    activity_kind: "text",
    detail_kind: "text",
    target_ref: { kind: "agent", id: "agent-1" },
    ...over,
  } as ActivityEvent;
}

function push(payload: {
  agent_id: string;
  event_id: string;
  event?: ActivityEvent;
}) {
  act(() => {
    wsHandlers.get("agent_activity:event")!(payload);
  });
}

describe("useAgentActivityEvents", () => {
  beforeEach(() => {
    wsHandlers.clear();
    reconnect.cb = null;
    queryState.pages = [[]];
    queryState.isLoading = false;
    queryState.isError = false;
    queryState.hasNextPage = false;
    queryState.isFetchingNextPage = false;
    queryState.fetchNextPage.mockClear();
    queryState.refetch.mockClear();
    clientHandles.invalidateQueries.mockClear();
  });

  // THE regression this hook's redesign exists for. A `wake_attempt` ("Message
  // received") is the first event of a round, so it lands while the Activity
  // panel's first-paint REST fetch is still in flight. The old path wrote WS
  // events straight into the query cache, so the moment that fetch resolved with
  // a pre-event snapshot it REPLACED the cache and dropped the wake — it only
  // reappeared on hard-refresh (REST always had it). Holding live events in
  // their own buffer means a REST replace can never drop one.
  it("keeps a live WS event when the first-paint REST fetch later resolves without it", () => {
    queryState.pages = [[]]; // fetch in-flight, cache still empty
    const { result, rerender } = renderHook(() => useAgentActivityEvents("agent-1"));

    // WS delivers the wake before REST first-paint lands.
    push({
      agent_id: "agent-1",
      event_id: "wake1",
      event: evt("wake1", "2026-07-13T14:20:56Z", {
        activity_kind: "wake_attempt",
        detail_kind: "task_dispatched",
      }),
    });
    expect(result.current.events.map((e) => e.id)).toEqual(["wake1"]);

    // The in-flight fetch resolves with a snapshot captured BEFORE the wake.
    act(() => {
      queryState.pages = [[evt("older", "2026-07-13T14:20:00Z")]];
      rerender();
    });

    // The wake must survive the REST replace (merged from the live buffer).
    expect(result.current.events.map((e) => e.id)).toEqual(["older", "wake1"]);
    expect(result.current.latest?.id).toBe("wake1");
  });

  it("appends further live events after first-paint, chronologically by id upsert", () => {
    queryState.pages = [[evt("older", "2026-07-13T14:20:00Z")]];
    const { result } = renderHook(() => useAgentActivityEvents("agent-1"));

    push({
      agent_id: "agent-1",
      event_id: "tool1",
      event: evt("tool1", "2026-07-13T14:21:10Z", {
        activity_kind: "tool_call",
        detail_kind: "tool_use",
      }),
    });
    push({
      agent_id: "agent-1",
      event_id: "text1",
      event: evt("text1", "2026-07-13T14:21:11Z"),
    });

    expect(result.current.events.map((e) => e.id)).toEqual(["older", "tool1", "text1"]);
  });

  it("replaces a row in place when a later WS push carries the same id (grown aggregate wins)", () => {
    queryState.pages = [
      [
        evt("t1", "2026-07-13T14:21:00Z", {
          activity_kind: "thinking",
          detail_kind: "thinking",
          text: "partial",
        }),
      ],
    ];
    const { result } = renderHook(() => useAgentActivityEvents("agent-1"));

    push({
      agent_id: "agent-1",
      event_id: "t1",
      event: evt("t1", "2026-07-13T14:21:00Z", {
        activity_kind: "thinking",
        detail_kind: "thinking",
        text: "partial and grown",
      }),
    });

    expect(result.current.events).toHaveLength(1);
    expect(result.current.events[0]!.text).toBe("partial and grown");
  });

  it("ignores a WS event for a different agent", () => {
    const { result } = renderHook(() => useAgentActivityEvents("agent-1"));
    push({
      agent_id: "agent-2",
      event_id: "x",
      event: evt("x", "2026-07-13T14:21:00Z"),
    });
    expect(result.current.events).toEqual([]);
  });

  it("resets the live buffer when the viewed agent changes (no cross-agent leak)", () => {
    const { result, rerender } = renderHook(
      ({ id }: { id: string }) => useAgentActivityEvents(id),
      { initialProps: { id: "agent-1" } },
    );
    push({
      agent_id: "agent-1",
      event_id: "a1",
      event: evt("a1", "2026-07-13T14:21:00Z"),
    });
    expect(result.current.events.map((e) => e.id)).toEqual(["a1"]);

    act(() => {
      rerender({ id: "agent-2" });
    });
    expect(result.current.events).toEqual([]);
  });

  it("falls back to invalidating the REST query for a degraded (event-less) push", () => {
    renderHook(() => useAgentActivityEvents("agent-1"));
    push({ agent_id: "agent-1", event_id: "z" });
    expect(clientHandles.invalidateQueries).toHaveBeenCalledWith({
      queryKey: ["agent-activity-events", "agent-1"],
    });
  });

  it("refetches the per-agent event query on WS reconnect (backfills the gap)", () => {
    renderHook(() => useAgentActivityEvents("agent-1"));
    expect(reconnect.cb).toBeTypeOf("function");
    act(() => {
      reconnect.cb!();
    });
    expect(clientHandles.invalidateQueries).toHaveBeenCalledWith({
      queryKey: ["agent-activity-events", "agent-1"],
    });
  });

  // #620: the first page (newest ~50) had no `Running command` row because a
  // high-frequency agent's newer rows pushed it into an older page. Loading the
  // older page must surface it. The hook flattens ALL fetched pages through the
  // reducer, so a command in a later-fetched (older) page appears in `events`,
  // ordered oldest-first regardless of page fetch order.
  it("surfaces a command from an older page once it is fetched (first page has none)", () => {
    queryState.pages = [
      [
        evt("s2", "2026-07-13T14:55:00Z", {
          activity_kind: "custom",
          detail_kind: "agent_status_changed",
        }),
        evt("s1", "2026-07-13T14:54:40Z", {
          activity_kind: "thinking",
          detail_kind: "thinking",
        }),
      ],
    ];
    queryState.hasNextPage = true;
    const { result, rerender } = renderHook(() => useAgentActivityEvents("agent-1"));

    // First page: no command reachable yet.
    expect(result.current.events.map((e) => e.id)).toEqual(["s1", "s2"]);
    expect(result.current.hasOlder).toBe(true);

    // Reader scrolls to top → the tab calls loadOlder → fetchNextPage.
    act(() => {
      result.current.loadOlder();
    });
    expect(queryState.fetchNextPage).toHaveBeenCalledTimes(1);

    // The older page arrives carrying the command; it now appears, ordered
    // oldest-first ahead of the newer status rows.
    act(() => {
      queryState.pages = [
        queryState.pages[0]!,
        [
          evt("cmd", "2026-07-13T14:54:31Z", {
            activity_kind: "tool_call",
            detail_kind: "tool_use",
            tool: "bash",
          }),
        ],
      ];
      queryState.hasNextPage = false;
      rerender();
    });
    expect(result.current.events.map((e) => e.id)).toEqual(["cmd", "s1", "s2"]);
  });

  it("does not fetch an older page when none remain or one is already loading", () => {
    queryState.pages = [[evt("a", "2026-07-13T14:21:00Z")]];
    queryState.hasNextPage = false;
    const { result, rerender } = renderHook(() => useAgentActivityEvents("agent-1"));

    act(() => result.current.loadOlder());
    expect(queryState.fetchNextPage).not.toHaveBeenCalled();

    act(() => {
      queryState.hasNextPage = true;
      queryState.isFetchingNextPage = true;
      rerender();
    });
    expect(result.current.hasOlder).toBe(true);
    expect(result.current.isLoadingOlder).toBe(true);
    act(() => result.current.loadOlder());
    expect(queryState.fetchNextPage).not.toHaveBeenCalled();
  });

  it("dedupes an id that appears in two pages and in a WS push (single row)", () => {
    // Same id in the newest page and an older page (e.g. a boundary row the
    // cursor re-returned) — the reducer must not double it.
    queryState.pages = [
      [evt("dup", "2026-07-13T14:30:00Z", { text: "page-new" })],
      [evt("dup", "2026-07-13T14:30:00Z", { text: "page-old" })],
    ];
    const { result } = renderHook(() => useAgentActivityEvents("agent-1"));
    expect(result.current.events).toHaveLength(1);

    // A WS push for that same id replaces it in place — still one row.
    push({
      agent_id: "agent-1",
      event_id: "dup",
      event: evt("dup", "2026-07-13T14:30:00Z", { text: "ws-grown" }),
    });
    expect(result.current.events).toHaveLength(1);
    expect(result.current.events[0]!.text).toBe("ws-grown");
  });
});
