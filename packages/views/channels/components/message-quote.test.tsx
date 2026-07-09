import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import type { ChannelMessage } from "@multica/core/types";
import { ComposerQuotePreview } from "./message-quote";

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

describe("ComposerQuotePreview", () => {
  it("shows sender, message type and a text summary", () => {
    render(
      <ComposerQuotePreview
        message={message({ content: "  Hello\nworld  " })}
        currentUserId="user-1"
        onCancel={vi.fn()}
      />,
    );

    expect(screen.getByTestId("composer-quote-preview")).toBeInTheDocument();
    expect(screen.getByText("Helper Bot")).toBeInTheDocument();
    expect(screen.getByText("Agent")).toBeInTheDocument();
    expect(screen.getByText("Hello world")).toBeInTheDocument();
  });

  it("summarizes image-only quotes without leaking empty content", () => {
    render(
      <ComposerQuotePreview
        message={message({
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
        })}
        currentUserId="user-1"
        onCancel={vi.fn()}
      />,
    );

    expect(screen.getByText("Image: photo.png")).toBeInTheDocument();
  });

  it("calls onCancel from the cancel control", async () => {
    const onCancel = vi.fn();
    render(
      <ComposerQuotePreview
        message={message({ content: "quoted" })}
        currentUserId="user-1"
        onCancel={onCancel}
      />,
    );

    await userEvent.click(screen.getByRole("button", { name: "Cancel quote" }));

    expect(onCancel).toHaveBeenCalledTimes(1);
  });
});
