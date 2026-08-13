import { describe, expect, it } from "vitest";
import {
  isStandaloneListItemOutstanding,
  isStandaloneSendOutstanding,
  isStandaloneSessionOutstanding,
  shouldClearChatPendingOnDone,
  standaloneStopRequiresInbox,
} from "./standalone-pending";

describe("standalone outstanding predicates", () => {
  it("keeps the bubble outstanding when send is pending true", () => {
    expect(isStandaloneSendOutstanding({ pending: true })).toBe(true);
    expect(isStandaloneSessionOutstanding({ pending: true })).toBe(true);
  });

  it("turns the indicator off only when pending is not true", () => {
    expect(isStandaloneSendOutstanding({ pending: false })).toBe(false);
    expect(isStandaloneSendOutstanding({})).toBe(false);
    expect(isStandaloneSessionOutstanding({})).toBe(false);
  });

  it("FAB list items run when pending is true for a session", () => {
    expect(
      isStandaloneListItemOutstanding({ pending: true, chat_session_id: "s1" }),
    ).toBe(true);
    expect(isStandaloneListItemOutstanding({ chat_session_id: "s1" })).toBe(false);
    expect(isStandaloneListItemOutstanding({ pending: true })).toBe(false);
  });

  it("clears outstanding on any chat:done for this session", () => {
    expect(shouldClearChatPendingOnDone()).toBe(true);
  });

  it("does not require an inbox event to stop", () => {
    expect(standaloneStopRequiresInbox()).toBe(false);
  });
});
