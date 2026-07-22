import { describe, expect, it } from "vitest";
import { QueryClient } from "@tanstack/react-query";
import type { ChannelMessage, ChannelMessagesPage } from "../types";
import type { InfiniteData } from "@tanstack/react-query";
import {
  buildOptimisticChannelMessage,
  markOptimisticChannelMessageFailed,
  removeOptimisticChannelMessage,
} from "./optimistic-send";
import {
  channelKeys,
  findChannelMessageMatchIndex,
  upsertChannelMessageInCache,
} from "./queries";

function page(messages: ChannelMessage[]): ChannelMessagesPage {
  return { messages, limit: 50, has_more: false };
}

function seedPage(qc: QueryClient, channelId: string, messages: ChannelMessage[]) {
  qc.setQueryData<InfiniteData<ChannelMessagesPage>>(channelKeys.messagesPage(channelId), {
    pages: [page(messages)],
    pageParams: [undefined],
  });
}

describe("optimistic send cache (LRM-222)", () => {
  it("matches by client_message_id so ACK replaces the temp bubble without a duplicate", () => {
    const optimistic = buildOptimisticChannelMessage({
      channelId: "c1",
      workspaceId: "w1",
      clientMessageId: "client-1",
      content: "hello",
      authorId: "u1",
      authorName: "Alice",
    });
    expect(optimistic.id).toBe("client-1");
    expect(optimistic.local_send_status).toBe("pending");

    const ack: ChannelMessage = {
      ...optimistic,
      id: "server-1",
      seq: 42,
      local_send_status: null,
      client_message_id: "client-1",
    };

    const list = [optimistic];
    expect(findChannelMessageMatchIndex(list, ack)).toBe(0);

    const qc = new QueryClient();
    seedPage(qc, "c1", [optimistic]);
    upsertChannelMessageInCache(qc, ack);

    const cached = qc.getQueryData<InfiniteData<ChannelMessagesPage>>(
      channelKeys.messagesPage("c1"),
    );
    const messages = cached?.pages[0]?.messages ?? [];
    expect(messages).toHaveLength(1);
    expect(messages[0]?.id).toBe("server-1");
    expect(messages[0]?.local_send_status ?? null).toBeNull();
    expect(messages[0]?.client_message_id).toBe("client-1");
  });

  it("marks a pending bubble failed and drops it on conflict remove", () => {
    const qc = new QueryClient();
    const optimistic = buildOptimisticChannelMessage({
      channelId: "c1",
      workspaceId: "w1",
      clientMessageId: "client-2",
      content: "retry me",
      authorId: "u1",
      authorName: "Alice",
    });
    seedPage(qc, "c1", [optimistic]);

    markOptimisticChannelMessageFailed(qc, "c1", "client-2");
    let messages =
      qc.getQueryData<InfiniteData<ChannelMessagesPage>>(channelKeys.messagesPage("c1"))?.pages[0]
        ?.messages ?? [];
    expect(messages[0]?.local_send_status).toBe("failed");

    removeOptimisticChannelMessage(qc, "c1", "client-2");
    messages =
      qc.getQueryData<InfiniteData<ChannelMessagesPage>>(channelKeys.messagesPage("c1"))?.pages[0]
        ?.messages ?? [];
    expect(messages).toHaveLength(0);
  });
});
