import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { api } from "@multica/core/api";
import { GraphMemoryCitationBadge } from "./graph-memory-citation-badge";

vi.mock("@multica/core/api", () => ({
  api: { getGraphMemoryMessageCitations: vi.fn() },
}));

vi.mock("../../i18n/use-t", () => ({
  useT: () => ({
    t: (selector: (root: any) => string, vars?: Record<string, string | number>) => {
      const value = selector({ graph_memory: {
        citations: "Memory sources",
        citation_version: "Graph v{version}",
        citation_loading: "Loading sources…",
        citation_empty: "No source snapshots are available",
      } });
      return Object.entries(vars ?? {}).reduce((text, [key, item]) => text.replace(`{${key}}`, String(item)), value);
    },
  }),
}));

describe("GraphMemoryCitationBadge", () => {
  beforeEach(() => vi.clearAllMocks());

  it("loads immutable citation snapshots only when the badge opens", async () => {
    vi.mocked(api.getGraphMemoryMessageCitations).mockResolvedValue({
      message_id: "message-1",
      items: [{
        id: "citation-1", node_id: "node-1", graph_version: 7, level: "1",
        epistemic_status: "supported", tags: ["routing"], title: "Dispatch",
        first_paragraph: "Dispatch detail", excerpt: "Dispatch detail", content_hash: "sha256:node-1",
        captured_at: "2026-08-25T00:00:00Z",
      }],
    });
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    render(
      <QueryClientProvider client={client}>
        <GraphMemoryCitationBadge workspaceId="workspace-1" messageId="message-1" count={1} />
      </QueryClientProvider>,
    );
    expect(api.getGraphMemoryMessageCitations).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole("button", { name: "Memory sources (1)" }));
    expect(await screen.findByText("Dispatch")).toBeInTheDocument();
    expect(screen.getByText("Graph v7")).toBeInTheDocument();
    expect(screen.getByText(/sha256:node-1/)).toBeInTheDocument();
    expect(api.getGraphMemoryMessageCitations).toHaveBeenCalledWith("workspace-1", "message-1");
  });
});
