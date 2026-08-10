import { describe, expect, it } from "vitest";
import { QueryClient } from "@tanstack/react-query";
import type { ChannelMessage, ChannelMessagesPage } from "../types";
import type { InfiniteData } from "@tanstack/react-query";
import {
  buildOptimisticChannelMessage,
  channelMessageListItemKey,
  markOptimisticChannelMessageFailed,
  removeOptimisticChannelMessage,
} from "./optimistic-send";
import {
  channelKeys,
  findChannelMessageMatchIndex,
  preserveLocalSendMessages,
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
  it("hydrates a voice recording attachment in the optimistic bubble", () => {
    const optimistic = buildOptimisticChannelMessage({
      channelId: "c1",
      workspaceId: "w1",
      clientMessageId: "voice-client-1",
      content: "spoken question",
      parts: [
        { type: "text", text: "spoken question" },
        {
          type: "voice",
          duration_ms: 1800,
          attachment_id: "recording-1",
          filename: "voice-recording.wav",
          content_type: "audio/wav",
          size_bytes: 64,
        },
      ],
      authorId: "u1",
      authorName: "Alice",
    });

    expect(optimistic.attachments).toEqual([
      expect.objectContaining({
        id: "recording-1",
        filename: "voice-recording.wav",
        content_type: "audio/wav",
        size_bytes: 64,
      }),
    ]);
  });

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

  it("does not put profile avatars into optimistic message state", () => {
    const optimistic = buildOptimisticChannelMessage({
      channelId: "c1",
      workspaceId: "w1",
      clientMessageId: "client-3",
      content: "face",
      authorId: "u1",
      authorName: "Alice",
    });
    expect(optimistic).not.toHaveProperty("author_avatar_url");
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

  it("matches a pending thread bubble when ACK omits client_message_id (LRM-271)", () => {
    const optimistic = buildOptimisticChannelMessage({
      channelId: "c1",
      workspaceId: "w1",
      clientMessageId: "client-thread-1",
      content: "hello thread",
      authorId: "u1",
      authorName: "Alice",
      threadRootMessageId: "root-1",
    });
    const ack: ChannelMessage = {
      ...optimistic,
      id: "server-thread-1",
      seq: 9,
      local_send_status: undefined,
      client_message_id: null,
      thread_root_message_id: "root-1",
    };

    expect(findChannelMessageMatchIndex([optimistic], ack)).toBe(0);

    const qc = new QueryClient();
    seedPage(qc, "c1", [optimistic]);
    upsertChannelMessageInCache(qc, ack);
    const messages =
      qc.getQueryData<InfiniteData<ChannelMessagesPage>>(channelKeys.messagesPage("c1"))?.pages[0]
        ?.messages ?? [];
    expect(messages).toHaveLength(1);
    expect(messages[0]?.id).toBe("server-thread-1");
    expect(messages[0]?.local_send_status ?? null).toBeNull();
    // ACK omitted client_message_id — preserve from the optimistic row for retry/identity.
    expect(messages[0]?.client_message_id).toBe("client-thread-1");
  });

  it("keeps a stable list item key across optimistic → ACK (LRM-273)", () => {
    const optimistic = buildOptimisticChannelMessage({
      channelId: "c1",
      workspaceId: "w1",
      clientMessageId: "client-stable",
      content: "hello",
      authorId: "u1",
      authorName: "Alice",
    });
    const ack: ChannelMessage = {
      ...optimistic,
      id: "server-stable",
      seq: 7,
      local_send_status: undefined,
      client_message_id: "client-stable",
    };
    expect(channelMessageListItemKey(optimistic)).toBe("client-stable");
    expect(channelMessageListItemKey(ack)).toBe("client-stable");

    const qc = new QueryClient();
    seedPage(qc, "c1", [optimistic]);
    upsertChannelMessageInCache(qc, {
      ...ack,
      client_message_id: null,
      local_send_status: "pending",
    });
    const messages =
      qc.getQueryData<InfiniteData<ChannelMessagesPage>>(channelKeys.messagesPage("c1"))?.pages[0]
        ?.messages ?? [];
    expect(messages).toHaveLength(1);
    expect(channelMessageListItemKey(messages[0]!)).toBe("client-stable");
    expect(messages[0]?.local_send_status ?? null).toBeNull();
  });

  it("preserves pending/failed bubbles across list refetch (LRM-280 silent-loss guard)", () => {
    const pending = buildOptimisticChannelMessage({
      channelId: "c1",
      workspaceId: "w1",
      clientMessageId: "client-inflight",
      content: "still sending",
      authorId: "u1",
      authorName: "Alice",
    });
    const failed = buildOptimisticChannelMessage({
      channelId: "c1",
      workspaceId: "w1",
      clientMessageId: "client-failed",
      content: "was failed",
      authorId: "u1",
      authorName: "Alice",
      status: "failed",
    });
    const serverOnly: ChannelMessage = {
      ...pending,
      id: "server-old",
      seq: 1,
      client_message_id: null,
      local_send_status: undefined,
      content: "already on server",
    };

    const merged = preserveLocalSendMessages([pending, failed, serverOnly], [serverOnly]);
    expect(merged.map((m) => m.id)).toEqual(["server-old", "client-inflight", "client-failed"]);
    expect(merged.find((m) => m.id === "client-inflight")?.local_send_status).toBe("pending");
    expect(merged.find((m) => m.id === "client-failed")?.local_send_status).toBe("failed");
  });

  it("drops preserved pending when the server already has the committed row (LRM-280)", () => {
    const pending = buildOptimisticChannelMessage({
      channelId: "c1",
      workspaceId: "w1",
      clientMessageId: "client-done",
      content: "landed",
      authorId: "u1",
      authorName: "Alice",
    });
    const committed: ChannelMessage = {
      ...pending,
      id: "server-done",
      seq: 10,
      local_send_status: undefined,
      client_message_id: "client-done",
    };

    const merged = preserveLocalSendMessages([pending], [committed]);
    expect(merged).toHaveLength(1);
    expect(merged[0]?.id).toBe("server-done");
    expect(merged[0]?.local_send_status ?? null).toBeNull();
  });
});
