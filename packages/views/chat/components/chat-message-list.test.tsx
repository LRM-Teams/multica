/**
 * @vitest-environment happy-dom
 */
import type { ReactNode } from "react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { ChatMessage } from "@multica/core/types";
import { renderWithI18n } from "../../test/i18n";
import { ChatMessageList } from "./chat-message-list";

vi.mock("../../common/use-viewing-timezone", () => ({
  useViewingTimezone: () => "UTC",
}));

vi.mock("react-virtuoso", () => ({
  Virtuoso: ({
    data = [],
    itemContent,
    components = {},
  }: {
    data?: ChatMessage[];
    itemContent: (index: number, item: ChatMessage) => ReactNode;
    components?: { Header?: () => ReactNode; Footer?: () => ReactNode };
  }) => {
    const Header = components.Header;
    const Footer = components.Footer;
    return (
      <div>
        {Header ? <Header /> : null}
        {data.map((item, index) => (
          <div key={item.id}>{itemContent(index, item)}</div>
        ))}
        {Footer ? <Footer /> : null}
      </div>
    );
  },
}));

function message(partial: Pick<ChatMessage, "id" | "role" | "content" | "created_at"> & Partial<ChatMessage>): ChatMessage {
  return {
    chat_session_id: "s1",
    task_id: null,
    ...partial,
  };
}

function renderList(messages: ChatMessage[]) {
  const qc = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  return renderWithI18n(
    <QueryClientProvider client={qc}>
      <div style={{ height: 400 }}>
        <ChatMessageList
          sessionId="s1"
          messages={messages}
          pendingTask={null}
          availability={undefined}
          hoverMessageActions
        />
      </div>
    </QueryClientProvider>,
  );
}

describe("ChatMessageList timestamps", () => {
  beforeEach(() => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date("2026-09-01T12:00:00.000Z"));
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  it("shows today's clock under user and assistant bubbles", () => {
    renderList([
      message({
        id: "u1",
        role: "user",
        content: "总结一下",
        created_at: "2026-09-01T05:42:42.000Z",
      }),
      message({
        id: "a1",
        role: "assistant",
        content: "汇报稿整理完成了。",
        created_at: "2026-09-01T05:39:11.000Z",
      }),
    ]);

    const times = screen.getAllByRole("time");
    expect(times.map((el) => el.textContent)).toEqual(["05:42", "05:39"]);
    expect(times[0]).toHaveAttribute("datetime", "2026-09-01T05:42:42.000Z");
  });

  it("buckets yesterday with the shared message-time label", () => {
    renderList([
      message({
        id: "u1",
        role: "user",
        content: "昨天的汇报",
        created_at: "2026-08-31T21:10:00.000Z",
      }),
    ]);

    expect(screen.getByRole("time")).toHaveTextContent("Yesterday 21:10");
  });

  it("skips a broken created_at", () => {
    renderList([
      message({
        id: "u1",
        role: "user",
        content: "no time",
        created_at: "not-a-date",
      }),
    ]);

    expect(screen.queryByRole("time")).not.toBeInTheDocument();
    expect(screen.getByText("no time")).toBeInTheDocument();
  });

  it("inserts a day divider when the local day changes", () => {
    renderList([
      message({
        id: "older",
        role: "assistant",
        content: "first day",
        created_at: "2026-08-31T22:00:00.000Z",
      }),
      message({
        id: "newer",
        role: "user",
        content: "next day",
        created_at: "2026-09-01T01:00:00.000Z",
      }),
    ]);

    const dividers = screen.getAllByTestId("date-divider");
    expect(dividers).toHaveLength(2);
    expect(dividers[0]).toHaveTextContent("Yesterday");
    expect(dividers[1]).toHaveTextContent("Today");
  });
});
