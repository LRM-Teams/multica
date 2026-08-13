import { render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import type { MessagePart } from "@multica/core/types";
import { MessageBody } from "./message-body";

// InlineReferenceContent does the real content+overlay projection (API/hovercards);
// mock it to a simple node that echoes the content it was asked to render.
// MessageBody resolves mentions for its compact preview (#530) — that goes through
// useActorName, which needs a QueryClient. These tests are about layout/parts, so
// stub the lookup rather than standing up a provider.
vi.mock("@multica/core/workspace/hooks", () => ({
  useActorName: () => ({ getActorName: (_t: string, _i: string, fb?: string) => fb ?? "Alice" }),
}));

vi.mock("../../common/inline-reference-content", () => ({
  InlineReferenceContent: ({ content, sourceMessageId }: { content: string; sourceMessageId?: string }) => (
    <div data-testid="inline-reference-content" data-source-message-id={sourceMessageId}>
      {content}
    </div>
  ),
}));

vi.mock("../../common/markdown", () => ({
  MemoizedMarkdown: ({ children }: { children: string }) => (
    <span data-testid="markdown-body">{children}</span>
  ),
}));

vi.mock("./message-parts-renderer", () => ({
  MessagePartsRenderer: () => <div data-testid="message-parts-body" />,
}));

function mentionRefPart(): MessagePart {
  return {
    type: "reference",
    ref_type: "mention",
    ref_subtype: "agent",
    ref_id: "agent-1",
    label: "@wendy_2",
    content_start_utf16: 0,
    content_end_utf16: 8,
  } as MessagePart;
}

function hiringProposalPart(): MessagePart {
  return {
    type: "reference",
    ref_type: "agent:create",
    ref_id: "fullstack-agent",
    label: "全栈开发",
    spans: [],
    params: { name: "全栈开发", status: "prepared" },
  } as MessagePart;
}

describe("MessageBody reference-only messages", () => {
  it("renders content when parts contain only a mention reference (agent @mentions)", () => {
    // Regression: reference-only parts + non-empty content used to collapse to an
    // empty bubble because hasBodyContent ignored reference overlays.
    render(<MessageBody content="@wendy_2 招人线继续推进，不等 MVP。" parts={[mentionRefPart()]} />);
    const body = screen.getByTestId("inline-reference-content");
    expect(body.textContent).toContain("招人线继续推进");
  });

  it("still renders nothing for a reference-only message with empty content", () => {
    const { container } = render(<MessageBody content="" parts={[mentionRefPart()]} />);
    expect(screen.queryByTestId("inline-reference-content")).toBeNull();
    expect(container.textContent).toBe("");
  });

  it("passes the owning row id to structured issue references", () => {
    render(
      <MessageBody
        content="See MUL-9"
        parts={[mentionRefPart()]}
        sourceMessageId="message-42"
      />,
    );

    expect(screen.getByTestId("inline-reference-content")).toHaveAttribute(
      "data-source-message-id",
      "message-42",
    );
  });

  it("suppresses the agent:create protocol fallback when the structured card exists", () => {
    render(
      <MessageBody
        content="[agent:create proposal] 全栈开发"
        parts={[hiringProposalPart()]}
        choiceContext={{ channelId: "channel-1", messageId: "message-1" }}
      />,
    );

    expect(screen.queryByText("[agent:create proposal] 全栈开发")).toBeNull();
    expect(screen.getByTestId("message-parts-body")).toBeInTheDocument();
  });

  it("suppresses the agent:create protocol fallback in compact previews", () => {
    render(
      <MessageBody
        compact
        content="[agent:create proposal] 全栈开发"
        parts={[hiringProposalPart()]}
      />,
    );

    expect(screen.queryByText("[agent:create proposal] 全栈开发")).toBeNull();
  });

  it("keeps instruction content when parts only carry a note_brief snapshot", () => {
    render(
      <MessageBody
        content="ship it"
        parts={[
          {
            type: "note_brief",
            ref_id: "page-1",
            label: "Launch",
            text: "Verify DM reply path.",
          },
        ]}
      />,
    );

    expect(screen.getByTestId("markdown-body").textContent).toContain("ship it");
    expect(screen.getByTestId("message-parts-body")).toBeInTheDocument();
  });
});
