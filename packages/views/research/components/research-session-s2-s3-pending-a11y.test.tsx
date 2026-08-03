// @vitest-environment jsdom

/**
 * LRM-1246 — S2 delete Confirm + S3 composer Stop: pending must stay
 * focusable via aria-disabled (not native disabled), same root as LRM-1213.
 */
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, it, vi, beforeEach } from "vitest";
import { fireEvent, render, screen, within } from "@testing-library/react";
import type { ResearchSession } from "@multica/core/types";
import enResearch from "../../locales/en/research.json";

const mutationRef = vi.hoisted(() => ({
  stop: {
    mutate: vi.fn(),
    isPending: false,
  },
  del: {
    mutate: vi.fn(),
    isPending: false,
  },
}));

vi.mock("@tanstack/react-query", () => ({
  useMutation: (opts: { mutationFn?: () => unknown }) => {
    const src = String(opts?.mutationFn ?? "");
    if (src.includes("deleteResearchSession") || src.includes("delete")) {
      return mutationRef.del;
    }
    return mutationRef.stop;
  },
  useQueryClient: () => ({
    invalidateQueries: vi.fn(),
  }),
}));

vi.mock("@multica/core/api", () => ({
  api: {
    stopResearchSession: vi.fn(),
    deleteResearchSession: vi.fn(),
  },
}));

vi.mock("@multica/core/hooks", () => ({
  useWorkspaceId: () => "workspace-1",
}));

vi.mock("@multica/core/research", () => ({
  researchKeys: {
    sessions: (wsId: string) => ["research", wsId, "sessions"],
    snapshot: (wsId: string, id: string) => ["research", wsId, "snapshot", id],
  },
}));

vi.mock("@multica/ui/lib/error-toast", () => ({
  showErrorToast: vi.fn(),
}));

vi.mock("sonner", () => ({
  toast: { success: vi.fn() },
}));

vi.mock("../../i18n/use-t", () => ({
  useT: () => ({
    t: (fn: (dict: typeof enResearch) => unknown) => fn(enResearch),
  }),
}));

vi.mock("../../navigation/app-link", () => ({
  AppLink: ({ children }: { children: React.ReactNode }) => <>{children}</>,
}));

import { ResearchSessionRowActions } from "./research-session-row-actions";

const here = path.dirname(fileURLToPath(import.meta.url));

function session(partial: Partial<ResearchSession> = {}): ResearchSession {
  return {
    id: "s1",
    workspace_id: "workspace-1",
    fleet_id: "fleet-1",
    created_by: "user-1",
    title: "Alpha",
    goal: "Map market",
    status: "running",
    current_stage: "s2_sources",
    project_id: null,
    channel_id: null,
    handoff_summary: null,
    created_at: "2026-07-30T00:00:00Z",
    updated_at: "2026-07-30T03:00:00Z",
    fleet_preview: [],
    ...partial,
  };
}

describe("LRM-1246 S2/S3 pending a11y", () => {
  beforeEach(() => {
    mutationRef.stop.mutate.mockReset();
    mutationRef.del.mutate.mockReset();
    mutationRef.stop.isPending = false;
    mutationRef.del.isPending = false;
  });

  it("S2: delete Confirm stays focusable via aria-disabled while pending", () => {
    const { rerender } = render(
      <ResearchSessionRowActions session={session()} />,
    );

    fireEvent.click(screen.getByLabelText(enResearch.actions.menu));
    fireEvent.click(screen.getByText(enResearch.actions.delete));

    mutationRef.del.isPending = true;
    rerender(<ResearchSessionRowActions session={session()} />);

    const confirm = screen.getByTestId("research-session-delete-confirm");
    expect(confirm).toHaveProperty("disabled", false);
    expect(confirm.getAttribute("aria-disabled")).toBe("true");

    const dialog = confirm.closest('[role="alertdialog"]') ?? confirm.parentElement!;
    const focusables = within(dialog as HTMLElement)
      .getAllByRole("button")
      .filter((el) => !(el as HTMLButtonElement).disabled);
    expect(focusables.length).toBeGreaterThan(0);

    fireEvent.click(confirm);
    expect(mutationRef.del.mutate).not.toHaveBeenCalled();
  });

  it("S3: composer Stop uses aria-disabled (not native disabled) while pending", () => {
    const src = fs.readFileSync(
      path.join(here, "research-session-page.tsx"),
      "utf8",
    );
    const stopBlock = src.slice(
      src.indexOf("data-testid=\"research-session-composer-stop\"") - 400,
      src.indexOf("data-testid=\"research-session-composer-stop\"") + 350,
    );
    expect(stopBlock).toContain("aria-disabled={stop.isPending");
    expect(stopBlock).not.toMatch(/disabled=\{stop\.isPending\}/);
    expect(stopBlock).toContain("if (stop.isPending) return");
  });
});
