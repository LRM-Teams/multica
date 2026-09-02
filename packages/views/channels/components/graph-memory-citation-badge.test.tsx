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
        citation_class_consolidated: "Consolidated memory",
        citation_class_recent_unreviewed: "Recent unreviewed observation",
        citation_class_historical: "Historical/superseded evidence",
        citation_class_restricted: "Restricted source",
        citation_class_retracted: "Retracted source",
        citation_restricted_note: "Details withheld for this restricted source.",
        citation_retracted_note: "This source was retracted; its content is unavailable.",
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

  it("labels all five citation classes and withholds restricted details and retraction sentinels", async () => {
    vi.mocked(api.getGraphMemoryMessageCitations).mockResolvedValue({
      message_id: "message-1",
      items: [
        {
          id: "citation-consolidated", node_id: "node-1", graph_version: 7, level: "1",
          epistemic_status: "supported", tags: ["routing"], title: "Dispatch roster",
          first_paragraph: "Current on-call rotation", excerpt: "Current on-call rotation",
          content_hash: "sha256:consolidated", captured_at: "2026-08-25T00:00:00Z",
        },
        {
          id: "citation-staging", node_id: "node-2", graph_version: 0, level: "-1",
          epistemic_status: "", tags: [], title: "",
          first_paragraph: "Raw staging observation", excerpt: "Raw staging observation",
          content_hash: "", captured_at: "2026-08-25T00:00:00Z",
        },
        {
          id: "citation-historical", node_id: "node-3", graph_version: 2, level: "2",
          epistemic_status: "superseded", tags: [], title: "Old process",
          first_paragraph: "Superseded steps", excerpt: "Superseded steps",
          content_hash: "sha256:historical", captured_at: "2026-08-25T00:00:00Z",
        },
        {
          id: "citation-restricted", node_id: "node-4", graph_version: 9, level: "3",
          epistemic_status: "accepted", tags: [], title: "",
          first_paragraph: "", excerpt: "",
          content_hash: "sha256:restricted", captured_at: "2026-08-25T00:00:00Z",
        },
        {
          id: "citation-retracted", node_id: "node-5", graph_version: 4, level: "1",
          epistemic_status: "supported", tags: [], title: "",
          first_paragraph: "content_retracted", excerpt: "content_retracted",
          content_hash: "sha256:retracted", captured_at: "2026-08-25T00:00:00Z",
        },
      ],
    });
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    render(
      <QueryClientProvider client={client}>
        <GraphMemoryCitationBadge workspaceId="workspace-1" messageId="message-1" count={5} />
      </QueryClientProvider>,
    );
    fireEvent.click(screen.getByRole("button", { name: "Memory sources (5)" }));

    expect(await screen.findByText("Consolidated memory")).toBeInTheDocument();
    expect(screen.getByText("Recent unreviewed observation")).toBeInTheDocument();
    expect(screen.getByText("Historical/superseded evidence")).toBeInTheDocument();
    expect(screen.getByText("Restricted source")).toBeInTheDocument();
    expect(screen.getByText("Retracted source")).toBeInTheDocument();

    // Reviewable classes keep rendering their captured content.
    expect(screen.getByText(/Current on-call rotation/)).toBeInTheDocument();
    expect(screen.getByText(/Raw staging observation/)).toBeInTheDocument();

    // Restricted citations must not leak excerpt or content hashes (story 31).
    expect(screen.getByText("Details withheld for this restricted source.")).toBeInTheDocument();
    expect(screen.queryByText(/sha256:restricted/)).not.toBeInTheDocument();

    // Retracted citations show a marker, never the sentinel body.
    expect(screen.getByText("This source was retracted; its content is unavailable.")).toBeInTheDocument();
    expect(screen.queryByText(/content_retracted/)).not.toBeInTheDocument();
  });
});
