import { InfiniteQueryObserver, QueryClient, QueryObserver } from "@tanstack/react-query";
import { beforeEach, describe, expect, it, vi } from "vitest";

/**
 * LRM-1296 (1260-P1) — switching channels must CANCEL the abandoned channel's
 * in-flight reads.
 *
 * React Query only aborts a fetch on last-observer-unsubscribe when the queryFn
 * actually *consumed* the `signal` it was handed (`#abortSignalConsumed`).
 * Before this fix none of the channel reads forwarded `signal` to `fetch`, so
 * A→B→C switching left A's and B's requests running to completion, holding
 * per-origin connection slots (HTTP/1.1 caps at 6) and server capacity while
 * C's message page — the one blocking first paint — queued behind them.
 */

type Captured = { signal?: AbortSignal } | undefined;

const captured: Record<string, Captured> = {};

function hang(name: string) {
  return (...args: unknown[]) => {
    // Options object is the last argument for every read under test.
    captured[name] = args[args.length - 1] as Captured;
    return new Promise<never>(() => {});
  };
}

const apiMock = vi.hoisted(() => ({
  listChannelMessagesPage: vi.fn(),
  listChannelMembers: vi.fn(),
  getChannelMemberManagementCapabilities: vi.fn(),
  listChannelMessageThread: vi.fn(),
  getChannelProject: vi.fn(),
  getChannelGoal: vi.fn(),
}));

vi.mock("../api", () => ({ api: apiMock }));

import { channelGoalOptions } from "./goal";
import { channelProjectOptions } from "./project";
import {
  channelMemberManagementCapabilitiesOptions,
  channelMembersOptions,
  channelMessageThreadOptions,
  channelMessagesPageOptions,
} from "./queries";

function client() {
  return new QueryClient({ defaultOptions: { queries: { retry: false } } });
}

describe("LRM-1296 — channel switch cancels stale reads", () => {
  beforeEach(() => {
    for (const key of Object.keys(captured)) delete captured[key];
    apiMock.listChannelMessagesPage.mockImplementation(hang("messagesPage"));
    apiMock.listChannelMembers.mockImplementation(hang("members"));
    apiMock.getChannelMemberManagementCapabilities.mockImplementation(hang("caps"));
    apiMock.listChannelMessageThread.mockImplementation(hang("thread"));
    apiMock.getChannelProject.mockImplementation(hang("project"));
    apiMock.getChannelGoal.mockImplementation(hang("goal"));
  });

  it("aborts the abandoned channel's message-page fetch when the observer unmounts", async () => {
    const qc = client();
    const observer = new InfiniteQueryObserver(
      qc,
      channelMessagesPageOptions("channel-a") as any,
    );
    const unsubscribe = observer.subscribe(() => {});
    await Promise.resolve();

    const signal = captured.messagesPage?.signal;
    expect(signal, "queryFn must forward React Query's AbortSignal to the API").toBeInstanceOf(
      AbortSignal,
    );
    expect(signal!.aborted).toBe(false);

    // Switch away: the per-channel query key means this observer unmounts.
    unsubscribe();
    expect(signal!.aborted).toBe(true);
  });

  const cases: [name: string, build: () => unknown][] = [
    ["members", () => channelMembersOptions("channel-a")],
    ["caps", () => channelMemberManagementCapabilitiesOptions("channel-a")],
    ["thread", () => channelMessageThreadOptions("channel-a", "root-1")],
    ["project", () => channelProjectOptions("ws-1", "channel-a")],
    ["goal", () => channelGoalOptions("channel-a")],
  ];

  it.each(cases)(
    "aborts the abandoned channel's %s fetch when the observer unmounts",
    async (name, build) => {
      const qc = client();
      const observer = new QueryObserver(qc, build() as any);
      const unsubscribe = observer.subscribe(() => {});
      await Promise.resolve();

      const signal = captured[name]?.signal;
      expect(signal, `${name} queryFn must forward React Query's AbortSignal`).toBeInstanceOf(
        AbortSignal,
      );
      unsubscribe();
      expect(signal!.aborted).toBe(true);
    },
  );
});
