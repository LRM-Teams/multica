import { describe, expect, it } from "vitest";
import { QueryClient } from "@tanstack/react-query";
import { chatKeys } from "./queries";
import { evictInactiveChatMessageCaches } from "./evict-inactive-caches";

describe("evictInactiveChatMessageCaches (LRM-1264)", () => {
  it("keeps listed sessions and drops other session message caches", () => {
    const qc = new QueryClient();
    qc.setQueryData(chatKeys.messagesPage("keep"), { pages: [], pageParams: [] });
    qc.setQueryData(chatKeys.messagesPage("bubble"), { pages: [], pageParams: [] });
    qc.setQueryData(chatKeys.messages("drop"), []);
    qc.setQueryData(chatKeys.pendingTask("drop"), {});
    qc.setQueryData(chatKeys.sessions("ws"), []);
    qc.setQueryData(chatKeys.taskMessages("task-1"), []);

    const removed = evictInactiveChatMessageCaches(qc, ["keep", "bubble", null]);
    expect(removed).toBe(2);
    expect(qc.getQueryData(chatKeys.messagesPage("keep"))).toBeTruthy();
    expect(qc.getQueryData(chatKeys.messagesPage("bubble"))).toBeTruthy();
    expect(qc.getQueryData(chatKeys.messages("drop"))).toBeUndefined();
    expect(qc.getQueryData(chatKeys.pendingTask("drop"))).toBeUndefined();
    expect(qc.getQueryData(chatKeys.sessions("ws"))).toEqual([]);
    expect(qc.getQueryData(chatKeys.taskMessages("task-1"))).toEqual([]);
  });
});
