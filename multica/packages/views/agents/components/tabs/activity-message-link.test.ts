// @vitest-environment node
import { describe, expect, it } from "vitest";
import type { ActivityEvent } from "./activity-event";
import { activityMessagePermalink } from "./activity-message-link";

const channelDetail = (id: string) => `/w/acme/channels/${id}`;

function outputEvent(overrides: Partial<ActivityEvent>): ActivityEvent {
  return {
    id: "e1",
    agent_id: "agent-1",
    occurred_at: "2026-07-13T10:00:00Z",
    activity_kind: "text",
    detail_kind: "output",
    target_ref: { kind: "channel", id: "chan-1" },
    source_refs: [{ kind: "message", id: "msg-1", seq: 5 }],
    ...overrides,
  };
}

describe("activityMessagePermalink", () => {
  it("builds a channel deep-link from target_ref channel id + message source ref", () => {
    expect(activityMessagePermalink(outputEvent({}), channelDetail)).toBe(
      "/w/acme/channels/chan-1?message=msg-1",
    );
  });

  it("returns null for a dm (no message-level route; target_ref.id is the chat_session_id)", () => {
    const e = outputEvent({ target_ref: { kind: "dm", id: "session-1" } });
    expect(activityMessagePermalink(e, channelDetail)).toBeNull();
  });

  it("returns null for a thread (v0 does not resolve thread routing yet)", () => {
    const e = outputEvent({ target_ref: { kind: "thread", id: "root-1", slug: "chan-1" } });
    expect(activityMessagePermalink(e, channelDetail)).toBeNull();
  });

  it("returns null when no message source ref is present (never infers channel from source_refs)", () => {
    const e = outputEvent({ source_refs: [{ kind: "issue", id: "iss-1" }] });
    expect(activityMessagePermalink(e, channelDetail)).toBeNull();
  });

  it("returns null when the channel target has no id", () => {
    const e = outputEvent({ target_ref: { kind: "channel" } });
    expect(activityMessagePermalink(e, channelDetail)).toBeNull();
  });

  it("url-encodes the message id", () => {
    const e = outputEvent({ source_refs: [{ kind: "message", id: "a b/c" }] });
    expect(activityMessagePermalink(e, channelDetail)).toBe(
      "/w/acme/channels/chan-1?message=a%20b%2Fc",
    );
  });
});
