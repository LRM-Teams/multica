import { describe, expect, it } from "vitest";
import { QueryClient } from "@tanstack/react-query";
import { channelKeys } from "./queries";
import { evictInactiveChannelMessageCaches } from "./evict-inactive-caches";

describe("evictInactiveChannelMessageCaches (LRM-1264)", () => {
  it("removes message/thread/search caches for other channels, keeps active + non-message keys", () => {
    const qc = new QueryClient();
    qc.setQueryData(channelKeys.messagesPage("active"), { pages: [], pageParams: [] });
    qc.setQueryData(channelKeys.messagesPage("other"), { pages: [], pageParams: [] });
    qc.setQueryData(channelKeys.messages("other"), []);
    qc.setQueryData(channelKeys.messageThread("other", "m1"), { messages: [] });
    qc.setQueryData(channelKeys.messageSearch("other", "hi"), { messages: [] });
    qc.setQueryData(channelKeys.members("other"), []);
    qc.setQueryData(channelKeys.list("ws"), []);

    const removed = evictInactiveChannelMessageCaches(qc, "active");
    expect(removed).toBe(4);
    expect(qc.getQueryData(channelKeys.messagesPage("active"))).toBeTruthy();
    expect(qc.getQueryData(channelKeys.messagesPage("other"))).toBeUndefined();
    expect(qc.getQueryData(channelKeys.messages("other"))).toBeUndefined();
    expect(qc.getQueryData(channelKeys.messageThread("other", "m1"))).toBeUndefined();
    expect(qc.getQueryData(channelKeys.messageSearch("other", "hi"))).toBeUndefined();
    expect(qc.getQueryData(channelKeys.members("other"))).toEqual([]);
    expect(qc.getQueryData(channelKeys.list("ws"))).toEqual([]);
  });

  it("with no active channel, drops every channel message-family cache", () => {
    const qc = new QueryClient();
    qc.setQueryData(channelKeys.messagesPage("a"), { pages: [], pageParams: [] });
    qc.setQueryData(channelKeys.messagesPage("b"), { pages: [], pageParams: [] });
    expect(evictInactiveChannelMessageCaches(qc, null)).toBe(2);
    expect(qc.getQueryData(channelKeys.messagesPage("a"))).toBeUndefined();
    expect(qc.getQueryData(channelKeys.messagesPage("b"))).toBeUndefined();
  });
});
