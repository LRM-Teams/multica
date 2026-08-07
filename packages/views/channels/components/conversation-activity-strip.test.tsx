// @vitest-environment jsdom

import { describe, expect, it, vi, beforeEach } from "vitest";
import type { ComponentProps } from "react";
import { render, screen } from "@testing-library/react";
import { I18nProvider } from "@multica/core/i18n/react";
import enChannels from "../../locales/en/channels.json";
import { ConversationActivityStrip } from "./conversation-activity-strip";

const TEST_RESOURCES = { en: { channels: enChannels } };

const mockWorkspaceId = vi.hoisted(() => "ws-1");
const mockQueries = vi.hoisted(() => vi.fn());
const mockInvalidateQueries = vi.hoisted(() => vi.fn());

vi.mock("@multica/core/hooks", () => ({
  useWorkspaceId: () => mockWorkspaceId,
}));

vi.mock("@multica/core/realtime", () => ({
  useWSEvent: () => undefined,
  useWSReconnect: () => undefined,
}));

vi.mock("@tanstack/react-query", async () => {
  const actual = await vi.importActual<typeof import("@tanstack/react-query")>(
    "@tanstack/react-query",
  );
  return {
    ...actual,
    useQueryClient: () => ({ invalidateQueries: mockInvalidateQueries }),
    useQueries: (...args: unknown[]) => mockQueries(...args),
  };
});

beforeEach(() => {
  mockQueries.mockReset();
  mockInvalidateQueries.mockReset();
});

function renderStrip(
  props: ComponentProps<typeof ConversationActivityStrip>,
  agentSummaries: Array<{ label: string; tone: string; visibility: string }> = [],
) {
  mockQueries.mockImplementation(({ queries }: { queries: Array<unknown> }) =>
    queries.map((_, index) => ({
      data: {
        summary: agentSummaries[index] ?? null,
      },
    })),
  );

  return render(
    <I18nProvider resources={TEST_RESOURCES} locale="en">
      <ConversationActivityStrip {...props} />
    </I18nProvider>,
  );
}

describe("ConversationActivityStrip", () => {
  it("renders human typing and one agent activity", () => {
    renderStrip(
      {
        typingActors: [{ actorName: "Alice" }],
        workingAgents: [{ id: "agent-1", displayName: "Kai" }],
      },
      [{ label: "Editing file...", tone: "warning", visibility: "visible" }],
    );

    expect(screen.getByText("Alice is typing")).toBeInTheDocument();
    expect(screen.getByText("Kai")).toBeInTheDocument();
    expect(screen.getByText("Editing file...")).toBeInTheDocument();
  });

  it("folds extra agents into a collapsed more line", () => {
    renderStrip(
      {
        workingAgents: [
          { id: "agent-1", displayName: "Kai" },
          { id: "agent-2", displayName: "Tess" },
          { id: "agent-3", displayName: "Rita" },
          { id: "agent-4", displayName: "Cindy" },
        ],
      },
      [
        { label: "Editing file...", tone: "warning", visibility: "visible" },
        { label: "Searching code...", tone: "info", visibility: "visible" },
        { label: "Thinking...", tone: "info", visibility: "visible" },
        { label: "Running command...", tone: "warning", visibility: "visible" },
      ],
    );

    expect(screen.getByText("Kai")).toBeInTheDocument();
    expect(screen.getByText("Tess")).toBeInTheDocument();
    expect(screen.getByText("Editing file...")).toBeInTheDocument();
    expect(screen.getByText("Searching code...")).toBeInTheDocument();
    expect(screen.getByText("2 more agents are working")).toBeInTheDocument();
    expect(screen.queryByText("Rita")).not.toBeInTheDocument();
    expect(screen.queryByText("Cindy")).not.toBeInTheDocument();
  });

  it("renders nothing when there is no typing or visible agent activity", () => {
    const { container } = renderStrip(
      { typingActors: [], workingAgents: [{ id: "agent-1", displayName: "Kai" }] },
      [{ label: "Online", tone: "success", visibility: "visible" }],
    );

    expect(container).toBeEmptyDOMElement();
  });
});
