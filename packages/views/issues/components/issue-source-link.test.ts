// @vitest-environment node
import { describe, it, expect } from "vitest";
import type { IssueSourceMessageRef } from "@multica/core/types";
import { issueSourceMessageHref } from "./issue-source-link";

const channelDetail = (id: string) => `/acme/channels/${id}`;

const groupSource: IssueSourceMessageRef = {
  channel_id: "chan-1",
  channel_name: "general",
  channel_kind: "group",
  message_id: "msg-1",
  thread_root_message_id: "msg-1",
  excerpt: "let's fix the login bug",
};

describe("issueSourceMessageHref", () => {
  it("builds a channel deep-link with the message query param", () => {
    expect(issueSourceMessageHref(groupSource, channelDetail)).toBe(
      "/acme/channels/chan-1?message=msg-1",
    );
  });

  it("routes dm sources through the same channel deep-link (no per-kind gate)", () => {
    expect(
      issueSourceMessageHref(
        { ...groupSource, channel_kind: "dm", channel_name: undefined },
        channelDetail,
      ),
    ).toBe("/acme/channels/chan-1?message=msg-1");
  });

  it("url-encodes the message id", () => {
    expect(
      issueSourceMessageHref({ ...groupSource, message_id: "a b/c" }, channelDetail),
    ).toBe("/acme/channels/chan-1?message=a%20b%2Fc");
  });

  it("returns null when there is no source", () => {
    expect(issueSourceMessageHref(undefined, channelDetail)).toBeNull();
  });
});
