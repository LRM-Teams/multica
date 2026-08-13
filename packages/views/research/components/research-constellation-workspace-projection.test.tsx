import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { ResearchConstellationWorkspace } from "./research-constellation-workspace";
import type { TypedGraphResponse } from "@multica/core/research";
import type { ResearchGraphNode } from "@multica/core/types";

vi.mock("@multica/ui/hooks/use-mobile", () => ({
  useIsMobile: () => false,
}));

vi.mock("@multica/core/research", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@multica/core/research")>();
  return {
    ...actual,
    useResearchCanvasStore: (
      selector: (state: {
        filter: ReturnType<typeof actual.emptyCanvasFilter>;
        viewport: null;
        setViewport: () => void;
      }) => unknown,
    ) =>
      selector({
        filter: actual.emptyCanvasFilter(),
        viewport: null,
        setViewport: vi.fn(),
      }),
    useResearchUiStore: (
      selector: (state: {
        d5RailOpen: boolean;
        setD5RailOpen: () => void;
        d5RailMode: "chat";
        setD5RailMode: () => void;
      }) => unknown,
    ) =>
      selector({
        d5RailOpen: true,
        setD5RailOpen: vi.fn(),
        d5RailMode: "chat",
        setD5RailMode: vi.fn(),
      }),
  };
});

vi.mock("../../i18n/use-t", () => ({
  useT: () => ({
    t: (selector: (bundle: Record<string, unknown>) => string) =>
      selector({
        d5: {
          canvas: {
            loading: "Loading constellation…",
            error: "Could not load the typed research graph.",
            stale_title: "Canvas may be stale.",
            stale_body: "Last loaded graph stays visible.",
            stale_announcement: "Canvas refresh failed.",
            recovered_announcement: "Canvas refreshed.",
            projection_mismatch_title: "Star-map projection unavailable",
            projection_mismatch_body: "Typed projection missing.",
            projection_mismatch_diagnostics:
              "Snapshot nodes: {{snapshotCount}} · Typed nodes: {{typedCount}}",
          },
          rail: { hide: "Hide", show: "Show", chat_tab: "Chat", detail_tab: "Detail" },
          summary: {
            title: "Constellation · {{loaded}} loaded directions",
            detail: "{{stable}} stable",
          },
          report: { title: "Node report", empty_summary: "No summary" },
        },
        session_page: { retry: "Retry" },
        interrupt: { retrying: "Retrying…" },
      }),
  }),
}));

class ResizeObserverMock {
  observe() {}
  unobserve() {}
  disconnect() {}
}

beforeEach(() => {
  vi.stubGlobal("ResizeObserver", ResizeObserverMock);
});

const snapshotNodes = [
  {
    id: "goal-1",
    session_id: "s1",
    node_type: "goal",
    title: "Goal",
    summary: "",
    status: "active",
    actor_agent_id: null,
    payload: {},
    child_ids: [],
    created_at: "",
    updated_at: "",
  },
] satisfies ResearchGraphNode[];

describe("ResearchConstellationWorkspace projection mismatch", () => {
  it("shows explicit error state when snapshot is ready but typed graph is empty", async () => {
    const onRetry = vi.fn();
    render(
      <ResearchConstellationWorkspace
        typedGraph={{
          session_id: "s1",
          graph_version: 0,
          total_node_count: 0,
          nodes: [],
          edges: [],
          clusters: [],
          lineage: {
            derived: {},
            merged: {},
            superseded: {},
            restarted: {},
            invalidated: {},
            supersedes: {},
          },
        }}
        typedLoading={false}
        typedError={false}
        projectionMismatch
        snapshotNodeCount={3}
        typedGraphSessionId="s1"
        onRetryTypedGraph={onRetry}
        snapshotNodes={snapshotNodes}
        selectedNode={null}
        onSelectNode={() => {}}
        executionRows={[]}
        onOpenAgentPanel={() => {}}
        canvasMode="ready"
        activeLens="relations"
        sources={[]}
        members={[]}
        chatPanel={<div>chat</div>}
        detailPanel={<div>detail</div>}
        composer={<div>composer</div>}
      />,
    );

    expect(screen.getByTestId("research-projection-mismatch")).toBeTruthy();
    expect(screen.getByTestId("research-projection-mismatch-diagnostics")).toBeTruthy();
    expect(screen.queryByTestId("star-graph-canvas")).toBeNull();

    await userEvent.click(screen.getByRole("button", { name: "Retry" }));
    expect(onRetry).toHaveBeenCalledTimes(1);
  });
});

describe("ResearchConstellationWorkspace local theme", () => {
  it("keeps a cached D5 canvas mounted and retryable after refresh failure", async () => {
    const onRetry = vi.fn();
    render(
      <ResearchConstellationWorkspace
        typedGraph={{
          session_id: "s1",
          graph_version: 1,
          total_node_count: 1,
          nodes: [
            {
              id: "goal-1",
              session_id: "s1",
              node_type: "goal",
              title: "Goal",
              level: "XXL",
              round: 1,
              status: "active",
              summary: "",
              actor_agent_id: null,
              cluster_id: null,
              confidence: null,
              document_count: 0,
              conclusion_count: 0,
              goal_version_id: null,
              derived_from: null,
              merged_from: [],
              superseded_by: null,
              restart_of: null,
              invalidated_by: null,
              superseded_at: null,
              invalidated_at: null,
              parent_id: null,
              child_ids: [],
              children_of: [],
              created_at: "",
              updated_at: "",
            },
          ],
          edges: [],
          clusters: [],
          lineage: {
            derived: {},
            merged: {},
            superseded: {},
            restarted: {},
            invalidated: {},
            supersedes: {},
          },
        } satisfies TypedGraphResponse}
        typedLoading={false}
        typedError
        onRetryTypedGraph={onRetry}
        snapshotNodes={snapshotNodes}
        selectedNode={null}
        onSelectNode={() => {}}
        executionRows={[]}
        onOpenAgentPanel={() => {}}
        canvasMode="ready"
        activeLens="relations"
        sources={[]}
        members={[]}
        chatPanel={<div>chat</div>}
        detailPanel={<div>detail</div>}
        composer={<div>composer</div>}
      />,
    );

    expect(screen.getByTestId("research-session-canvas-host").className).toContain("d5-canvas-host");
    expect(screen.getByTestId("star-graph-canvas")).toBeTruthy();
    expect(screen.getByTestId("research-canvas-stale-notice")).toBeTruthy();
    await userEvent.click(screen.getByTestId("research-canvas-stale-retry"));
    expect(onRetry).toHaveBeenCalledTimes(1);
  });
});
