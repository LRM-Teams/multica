import { QueryClient } from "@tanstack/react-query";
import { describe, expect, it } from "vitest";
import { applyMemberPresenceEvent } from "./use-member-presence";
import { workspaceKeys } from "./queries";
import type { MemberPresenceResponse } from "../types/workspace";

describe("applyMemberPresenceEvent", () => {
  it("adds online and removes offline entries", () => {
    const qc = new QueryClient();
    const key = workspaceKeys.memberPresence("ws-1");
    qc.setQueryData<MemberPresenceResponse>(key, { members: [] });

    applyMemberPresenceEvent(qc, "ws-1", {
      user_id: "u1",
      status: "online",
      observed_at: "2026-07-23T00:00:00Z",
    });
    expect(qc.getQueryData<MemberPresenceResponse>(key)?.members).toEqual([
      { user_id: "u1", status: "online", observed_at: "2026-07-23T00:00:00Z" },
    ]);

    applyMemberPresenceEvent(qc, "ws-1", {
      user_id: "u1",
      status: "offline",
    });
    expect(qc.getQueryData<MemberPresenceResponse>(key)?.members).toEqual([]);
  });
});
