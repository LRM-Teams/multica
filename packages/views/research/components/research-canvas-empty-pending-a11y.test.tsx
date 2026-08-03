import { beforeEach, describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen } from "@testing-library/react";
import { ResearchCanvasEmptyState } from "./research-canvas-empty-state";

const mutationRef = vi.hoisted(() => ({
  current: {
    mutate: vi.fn(),
    isPending: false,
    isError: false,
    error: null as unknown,
    reset: vi.fn(),
  },
}));

vi.mock("@tanstack/react-query", () => ({
  useMutation: () => mutationRef.current,
  useQueryClient: () => ({
    setQueryData: vi.fn(),
    invalidateQueries: vi.fn(),
  }),
}));

vi.mock("@multica/core/api", () => ({
  api: { createResearchSession: vi.fn() },
}));

vi.mock("@multica/core/hooks", () => ({
  useWorkspaceId: () => "workspace-1",
}));

vi.mock("@multica/core/paths", () => ({
  useWorkspacePaths: () => ({
    research: () => "/ws/research",
    researchDetail: (id: string) => `/ws/research/${id}`,
  }),
}));

vi.mock("@multica/core/research", () => ({
  researchKeys: {
    snapshot: (wsId: string, id: string) => ["research", wsId, "snapshot", id],
    sessions: (wsId: string) => ["research", wsId, "sessions"],
  },
}));

vi.mock("../../navigation/context", () => ({
  useNavigation: () => ({ push: vi.fn() }),
}));

vi.mock("../../i18n/use-t", () => ({
  useT: () => ({
    t: (fn: (dict: Record<string, unknown>) => unknown) =>
      fn({
        node: { goal: "Goal", probe: "Probe" },
        logic: { lane: { source: "Source" } },
        session_page: {
          canvas_empty_title: "Empty canvas",
          canvas_empty_body: "Start a session.",
          canvas_empty_home: "Home",
          canvas_empty_create: "Create session",
          canvas_empty_creating: "Creating…",
          canvas_empty_create_goal: "Explore the empty canvas",
        },
      }),
  }),
}));

describe("ResearchCanvasEmptyState pending a11y (LRM-1241)", () => {
  beforeEach(() => {
    mutationRef.current = {
      mutate: vi.fn(),
      isPending: false,
      isError: false,
      error: null,
      reset: vi.fn(),
    };
  });

  it("keeps create focusable while pending: aria-disabled, not native disabled", () => {
    mutationRef.current.isPending = true;
    render(<ResearchCanvasEmptyState />);
    const create = screen.getByTestId("research-canvas-empty-create") as HTMLButtonElement;
    expect(create.hasAttribute("disabled")).toBe(false);
    expect(create.disabled).toBe(false);
    expect(create.getAttribute("aria-disabled")).toBe("true");
    expect(screen.getByText("Creating…")).toBeTruthy();
  });

  it("swallows repeat activation while pending; mutates once when idle", () => {
    const mutate = vi.fn();
    mutationRef.current = { ...mutationRef.current, mutate, isPending: false };
    const { rerender } = render(<ResearchCanvasEmptyState />);
    const create = screen.getByTestId("research-canvas-empty-create");
    create.focus();
    fireEvent.click(create);
    expect(mutate).toHaveBeenCalledTimes(1);

    mutationRef.current = { ...mutationRef.current, mutate, isPending: true };
    rerender(<ResearchCanvasEmptyState />);
    const pending = screen.getByTestId("research-canvas-empty-create") as HTMLButtonElement;
    expect(pending.getAttribute("aria-disabled")).toBe("true");
    expect(pending.hasAttribute("disabled")).toBe(false);
    expect(document.activeElement === pending || pending.tabIndex >= 0).toBe(true);
    fireEvent.click(pending);
    fireEvent.keyDown(pending, { key: "Enter" });
    expect(mutate).toHaveBeenCalledTimes(1);
  });
});
