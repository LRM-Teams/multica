import { describe, expect, it } from "vitest";
import type { InfiniteData } from "@tanstack/react-query";
import type { ChannelMessagesPage } from "@multica/core/types";
import { isDmTimelineBehindPreview } from "./dm-timeline-freshness";

function pages(createdAt: string): InfiniteData<ChannelMessagesPage> {
  return {
    pages: [
      {
        messages: [
          {
            id: "cached-message",
            channel_id: "dm-1",
            workspace_id: "ws-1",
            seq: 1,
            type: "agent",
            author_id: "agent-1",
            author_name: "Agent",
            content: "cached reply",
            source: "multica",
            external_message_id: null,
            client_message_id: null,
            created_at: createdAt,
          },
        ],
        has_more: false,
        next_cursor: null,
        limit: 50,
      },
    ],
    pageParams: [null],
  };
}

describe("isDmTimelineBehindPreview (LRM-1433)", () => {
  it("detects a retained timeline whose newest bubble predates the DM preview", () => {
    expect(
      isDmTimelineBehindPreview(
        pages("2026-08-04T12:18:00Z"),
        "2026-08-04T12:19:00Z",
      ),
    ).toBe(true);
  });

  it("does not refresh when the preview is already represented in the timeline", () => {
    expect(
      isDmTimelineBehindPreview(
        pages("2026-08-04T12:19:00Z"),
        "2026-08-04T12:19:00Z",
      ),
    ).toBe(false);
  });

  it("does not treat a cold, not-yet-loaded timeline as stale cache", () => {
    expect(isDmTimelineBehindPreview(undefined, "2026-08-04T12:19:00Z")).toBe(false);
  });
});
