import { describe, expect, it } from "vitest";
import { QueryClient } from "@tanstack/react-query";
import { channelKeys } from "./queries";
import { evictInactiveChannelMessageCaches } from "./evict-inactive-caches";

describe("evictInactiveChannelMessageCaches (LRM-1363)", () => {
  it("is a no-op: keeps other channels' message/thread/search caches", () => {
    const qc = new QueryClient();
    qc.setQueryData(channelKeys.messagesPage("active"), { pages: [], pageParams: [] });
    qc.setQueryData(channelKeys.messagesPage("other"), { pages: [], pageParams: [] });
    qc.setQueryData(channelKeys.messages("other"), []);
    qc.setQueryData(channelKeys.messageThread("other", "m1"), { messages: [] });
    qc.setQueryData(channelKeys.messageSearch("other", "hi"), { messages: [] });
    qc.setQueryData(channelKeys.members("other"), []);
    qc.setQueryData(channelKeys.list("ws"), []);

    const removed = evictInactiveChannelMessageCaches(qc, "active");
    expect(removed).toBe(0);
    expect(qc.getQueryData(channelKeys.messagesPage("active"))).toBeTruthy();
    expect(qc.getQueryData(channelKeys.messagesPage("other"))).toBeTruthy();
    expect(qc.getQueryData(channelKeys.messages("other"))).toEqual([]);
    expect(qc.getQueryData(channelKeys.messageThread("other", "m1"))).toEqual({
      messages: [],
    });
    expect(qc.getQueryData(channelKeys.messageSearch("other", "hi"))).toEqual({
      messages: [],
    });
    expect(qc.getQueryData(channelKeys.members("other"))).toEqual([]);
    expect(qc.getQueryData(channelKeys.list("ws"))).toEqual([]);
  });

  it("with no active channel still keeps message-family caches", () => {
    const qc = new QueryClient();
    qc.setQueryData(channelKeys.messagesPage("a"), { pages: [], pageParams: [] });
    qc.setQueryData(channelKeys.messagesPage("b"), { pages: [], pageParams: [] });
    expect(evictInactiveChannelMessageCaches(qc, null)).toBe(0);
    expect(qc.getQueryData(channelKeys.messagesPage("a"))).toBeTruthy();
    expect(qc.getQueryData(channelKeys.messagesPage("b"))).toBeTruthy();
  });
});
