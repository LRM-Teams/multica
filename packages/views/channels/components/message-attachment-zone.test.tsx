import { render, screen, within } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import type { Attachment, MessagePart } from "@multica/core/types";
import { MessageAttachmentZone } from "./message-attachment-zone";
import { MessageBody } from "./message-body";

vi.mock("../../editor", () => ({
  Attachment: ({
    attachment,
  }: {
    attachment: { kind: string; attachment?: { id: string; filename: string; content_type: string } };
  }) => {
    if (attachment.kind === "record" && attachment.attachment) {
      const a = attachment.attachment;
      const isImage = a.content_type.startsWith("image/");
      return (
        <div
          data-testid={isImage ? "attachment-image" : "attachment-file"}
          data-attachment-id={a.id}
        >
          {a.filename}
        </div>
      );
    }
    return null;
  },
  AttachmentDownloadProvider: ({ children }: { children: React.ReactNode }) => <>{children}</>,
}));

vi.mock("../../common/markdown", () => ({
  MemoizedMarkdown: ({ children }: { children: string }) => (
    <span data-testid="markdown-body">{children}</span>
  ),
}));

vi.mock("./message-parts-renderer", () => ({
  MessagePartsRenderer: ({ parts }: { parts: MessagePart[] }) => (
    <div data-testid="message-parts-body">
      {parts.map((part) =>
        part.type === "text" ? (
          <span key={`text:${part.text}`} data-testid="body-text">
            {part.text}
          </span>
        ) : part.type === "sticker" ? (
          <span key={`sticker:${part.sticker_id}`} data-testid="body-sticker">
            sticker
          </span>
        ) : part.type === "attachment" ? (
          <span key={`att:${part.attachment_id}`} data-testid="body-attachment" />
        ) : null,
      )}
    </div>
  ),
}));

vi.mock("../../i18n/use-t", () => ({
  useT: () => ({
    t: (
      selector: (resources: {
        message: { attachment_unavailable: string };
      }) => string,
    ) =>
      selector({
        message: {
          attachment_unavailable: "Attachment unavailable",
        },
      }),
  }),
}));

function makeAttachment(overrides: Partial<Attachment> & Pick<Attachment, "id" | "filename" | "content_type">): Attachment {
  return {
    workspace_id: "w1",
    issue_id: null,
    comment_id: null,
    chat_session_id: null,
    chat_message_id: null,
    uploader_type: "user",
    uploader_id: "u1",
    url: `/files/${overrides.id}`,
    download_url: `/files/${overrides.id}`,
    markdown_url: `/api/attachments/${overrides.id}/download`,
    size_bytes: 100,
    created_at: "2026-07-13T00:00:00Z",
    ...overrides,
  };
}

describe("MessageAttachmentZone", () => {
  it("renders two image attachments as thumbs in attachment-part order", () => {
    const parts: MessagePart[] = [
      { type: "text", text: "check these" },
      { type: "attachment", attachment_id: "img-a" },
      { type: "attachment", attachment_id: "img-b" },
    ];
    const attachments = [
      makeAttachment({ id: "img-b", filename: "b.png", content_type: "image/png" }),
      makeAttachment({ id: "img-a", filename: "a.png", content_type: "image/png" }),
    ];

    render(<MessageAttachmentZone parts={parts} attachments={attachments} />);

    const zone = screen.getByTestId("message-attachment-zone");
    const images = within(zone).getAllByTestId("attachment-image");
    expect(images).toHaveLength(2);
    expect(images[0]).toHaveAttribute("data-attachment-id", "img-a");
    expect(images[1]).toHaveAttribute("data-attachment-id", "img-b");
    expect(within(zone).queryByText("check these")).not.toBeInTheDocument();
  });

  it("renders non-image attachments as file tiles", () => {
    const parts: MessagePart[] = [
      { type: "attachment", attachment_id: "doc-1" },
    ];
    const attachments = [
      makeAttachment({
        id: "doc-1",
        filename: "spec.pdf",
        content_type: "application/pdf",
      }),
    ];

    render(<MessageAttachmentZone parts={parts} attachments={attachments} />);

    expect(screen.getByTestId("attachment-file")).toHaveTextContent("spec.pdf");
    expect(screen.queryByTestId("attachment-image")).not.toBeInTheDocument();
  });

  it("renders a safe placeholder when hydration is missing", () => {
    const parts: MessagePart[] = [
      {
        type: "attachment",
        attachment_id: "missing-1",
        filename: "secret-should-not-leak.pdf",
      },
    ];

    render(<MessageAttachmentZone parts={parts} attachments={[]} />);

    const placeholder = screen.getByTestId("attachment-unavailable");
    expect(placeholder).toHaveTextContent("Attachment unavailable");
    expect(placeholder).not.toHaveTextContent("secret-should-not-leak");
  });

  it("returns null when there are no attachment parts", () => {
    const { container } = render(
      <MessageAttachmentZone
        parts={[{ type: "text", text: "hello" }]}
        attachments={[
          makeAttachment({ id: "orphan", filename: "x.png", content_type: "image/png" }),
        ]}
      />,
    );
    expect(container).toBeEmptyDOMElement();
  });

  it("preserves mixed image/file order from attachment parts", () => {
    const parts: MessagePart[] = [
      { type: "attachment", attachment_id: "img-1" },
      { type: "attachment", attachment_id: "file-1" },
      { type: "attachment", attachment_id: "img-2" },
    ];
    const attachments = [
      makeAttachment({ id: "img-2", filename: "2.png", content_type: "image/png" }),
      makeAttachment({ id: "file-1", filename: "notes.txt", content_type: "text/plain" }),
      makeAttachment({ id: "img-1", filename: "1.png", content_type: "image/png" }),
    ];

    render(<MessageAttachmentZone parts={parts} attachments={attachments} />);

    const zone = screen.getByTestId("message-attachment-zone");
    const items = zone.querySelectorAll("[data-attachment-id], [data-testid='attachment-unavailable']");
    expect(items[0]).toHaveAttribute("data-attachment-id", "img-1");
    expect(items[1]).toHaveAttribute("data-attachment-id", "file-1");
    expect(items[2]).toHaveAttribute("data-attachment-id", "img-2");
  });
});

describe("MessageBody + attachment zone layout", () => {
  it("renders text body first, then the attachment zone (never interleaved)", () => {
    const parts: MessagePart[] = [
      { type: "text", text: "see screenshots" },
      { type: "attachment", attachment_id: "img-a" },
      { type: "attachment", attachment_id: "img-b" },
    ];
    const attachments = [
      makeAttachment({ id: "img-a", filename: "a.png", content_type: "image/png" }),
      makeAttachment({ id: "img-b", filename: "b.png", content_type: "image/png" }),
    ];

    render(
      <MessageBody content="see screenshots" parts={parts} attachments={attachments} />,
    );

    const bodyText = screen.getByTestId("body-text");
    const zone = screen.getByTestId("message-attachment-zone");
    expect(bodyText).toHaveTextContent("see screenshots");
    expect(within(zone).getAllByTestId("attachment-image")).toHaveLength(2);

    // Zone must follow body in DOM order (Slack: body then attachments).
    const position = bodyText.compareDocumentPosition(zone);
    expect(position & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
  });

  it("attachment-only messages render the zone without empty body chrome", () => {
    const parts: MessagePart[] = [
      { type: "attachment", attachment_id: "img-a" },
    ];
    const attachments = [
      makeAttachment({ id: "img-a", filename: "solo.png", content_type: "image/png" }),
    ];

    render(<MessageBody content="" parts={parts} attachments={attachments} />);

    expect(screen.queryByTestId("body-text")).not.toBeInTheDocument();
    expect(screen.getByTestId("message-attachment-zone")).toBeInTheDocument();
    expect(screen.getByTestId("attachment-image")).toHaveTextContent("solo.png");
  });
});
