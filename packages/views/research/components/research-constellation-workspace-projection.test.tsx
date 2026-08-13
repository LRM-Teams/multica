import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { ResearchConstellationWorkspace } from "./research-constellation-workspace";
import type { TypedGraphResponse } from "@multica/core/research";
import type { ResearchGraphNode } from "@multica/core/types";
import enResearch from "../../locales/en/research.json";

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
    t: (selector: (bundle: typeof enResearch) => unknown, vars?: Record<string, unknown>) => {
      const raw = selector(enResearch);
      if (typeof raw !== "string" || !vars) return raw;
      return raw.replace(/\{\{(\w+)\}\}/g, (_, key: string) => String(vars[key] ?? ""));
    },
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

  it("keeps projection retry focused while a gateway retry is pending", async () => {
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
        retryTypedGraphPending
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

    const retry = screen.getByRole("button", { name: "Retrying…" });
    expect(retry).toHaveAttribute("aria-disabled", "true");
    expect(retry).not.toBeDisabled();
    retry.focus();
    await userEvent.click(retry);
    expect(document.activeElement).toBe(retry);
    expect(onRetry).not.toHaveBeenCalled();
  });
});

describe("ResearchConstellationWorkspace typed graph recovery", () => {
  it("offers a retry action when the initial typed graph request fails", async () => {
    const onRetry = vi.fn();
    render(
      <ResearchConstellationWorkspace
        typedGraph={undefined}
        typedLoading={false}
        typedError
        onRetryTypedGraph={onRetry}
        snapshotNodes={[]}
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

    expect(screen.getByTestId("research-typed-graph-error")).toBeTruthy();
    await userEvent.click(screen.getByRole("button", { name: "Retry" }));
    expect(onRetry).toHaveBeenCalledTimes(1);
  });

  it("disables retry while typed graph recovery is pending", () => {
    render(
      <ResearchConstellationWorkspace
        typedGraph={undefined}
        typedLoading={false}
        typedError
        onRetryTypedGraph={() => {}}
        retryTypedGraphPending
        snapshotNodes={[]}
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

    expect(screen.getByRole("button", { name: "Retrying…" })).toBeDisabled();
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
