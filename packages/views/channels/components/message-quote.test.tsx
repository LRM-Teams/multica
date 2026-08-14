import { render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import type { ChannelMessage } from "@multica/core/types";
import { buildEditableMessageQuoteText, MessageQuoteCard } from "./message-quote";

vi.mock("@multica/core/workspace/hooks", () => ({
  useActorName: () => ({
    getActorName: (_type: string, id: string, fallback?: string) =>
      id === "agent-1" ? "Helper Bot" : fallback,
  }),
}));

vi.mock("../../i18n/use-t", () => ({
  useT: () => ({
    t: (
      selector: (resources: {
        quote: {
          action: string;
          jump_to: string;
          cancel: string;
          unavailable_title: string;
          unavailable_summary: string;
          type_user: string;
          type_agent: string;
          type_lark: string;
          type_system: string;
          type_unknown: string;
          attachment_summary: string;
          attachments_summary: string;
          image_summary: string;
          images_summary: string;
          empty_summary: string;
        };
      }) => string,
      vars?: Record<string, number>,
    ) =>
      selector({
        quote: {
          action: "Quote",
          jump_to: "Jump to original message",
          cancel: "Cancel quote",
          unavailable_title: "Original unavailable",
          unavailable_summary: "No access",
          type_user: "Message",
          type_agent: "Agent",
          type_lark: "Feishu",
          type_system: "System",
          type_unknown: "Message",
          attachment_summary: "Attachment",
          attachments_summary: `${vars?.count ?? 0} attachments`,
          image_summary: "Image",
          images_summary: `${vars?.count ?? 0} images`,
          empty_summary: "No preview available",
        },
      }),
  }),
}));

function message(overrides: Partial<ChannelMessage> = {}): ChannelMessage {
  return {
    id: "m1",
    channel_id: "c1",
    workspace_id: "w1",
    seq: 1,
    type: "agent",
    author_id: "agent-1",
    author_name: "agent",
    content: "",
    source: "multica",
    external_message_id: null,
    client_message_id: null,
    created_at: "2026-07-09T00:00:00Z",
    ...overrides,
  };
}

const quoteLabels = {
  attachment: "Attachment",
  attachments: (count: number) => `${count} attachments`,
  image: "Image",
  images: (count: number) => `${count} images`,
  empty: "No preview available",
};
const resolveMention = (_type: "member" | "agent", id: string, fallback: string) =>
  id === "agent-1" ? "Helper Bot" : fallback;

describe("buildEditableMessageQuoteText", () => {
  it("builds author-free literal text for editable composer content", () => {
    expect(
      buildEditableMessageQuoteText(
        message({ content: "  Hello\nworld  " }),
        quoteLabels,
        resolveMention,
      ),
    ).toBe("> Hello world");
  });

  it("resolves a quoted mention to the display name — no internal handle (#530)", () => {
    expect(
      buildEditableMessageQuoteText(
        message({
          content: "cc @agent_123 pls",
          parts: [
            {
              type: "reference",
              ref_type: "mention",
              ref_subtype: "agent",
              ref_id: "agent-1",
              label: "@agent_123",
              content_start_utf16: 3,
              content_end_utf16: 13,
            },
          ],
        } as never),
        quoteLabels,
        resolveMention,
      ),
    ).toBe("> cc @Helper Bot pls");
  });

  it("projects a quoted channel reference to its readable channel name (LRM-1431)", () => {
    const raw = "[pr-ops](mention://channel/channel-1)";
    expect(
      buildEditableMessageQuoteText(
        message({
          content: `ask ${raw}`,
          parts: [
            {
              type: "reference",
              ref_type: "channel-ref",
              ref_id: "channel-1",
              label: "pr-ops",
              content_start_utf16: 4,
              content_end_utf16: 4 + raw.length,
            },
          ],
        } as never),
        quoteLabels,
        resolveMention,
      ),
    ).toBe("> ask #pr-ops");
  });

  it("builds a safe editable summary for an image-only message", () => {
    expect(
      buildEditableMessageQuoteText(
        message({
          content: "",
          attachments: [
            {
              id: "att-1",
              workspace_id: "w1",
              issue_id: null,
              comment_id: null,
              chat_session_id: null,
              chat_message_id: null,
              uploader_type: "user",
              uploader_id: "user-1",
              filename: "photo.png",
              url: "/photo.png",
              download_url: "/photo.png",
              markdown_url: "/photo.png",
              content_type: "image/png",
              size_bytes: 10,
              created_at: "2026-07-09T00:00:00Z",
            },
          ],
        }),
        quoteLabels,
        resolveMention,
      ),
    ).toBe("> Image: photo.png");
  });
});

describe("MessageQuoteCard", () => {
  it("renders quote snapshots with live sender identity and type", () => {
    render(
      <MessageQuoteCard
        quote={{
          messageId: "m1",
          status: "active",
          snapshot: {
            type: "agent",
            authorId: "agent-1",
            authorName: "agent",
            content: "  Hello\nworld  ",
            createdAt: "2026-07-09T00:00:00Z",
          },
        }}
        quoteMessageId="m1"
        currentUserId="user-1"
      />,
    );

    expect(screen.getByText("Helper Bot")).toBeInTheDocument();
    expect(screen.getByText(/Hello world/)).toBeInTheDocument();
    expect(screen.getByTestId("message-quote-card")).toHaveTextContent(
      "> Helper Bot: Hello world",
    );
  });

  it("renders a private fallback for deleted or inaccessible quote snapshots", () => {
    render(
      <MessageQuoteCard
        quote={{ messageId: "m1", status: "deleted" }}
        quoteMessageId="m1"
        currentUserId="user-1"
      />,
    );

    expect(screen.getByText("Original unavailable")).toBeInTheDocument();
    expect(screen.getByText("No access")).toBeInTheDocument();
  });
});
