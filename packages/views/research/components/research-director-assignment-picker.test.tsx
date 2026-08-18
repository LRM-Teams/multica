import { describe, expect, it, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import { ResearchDirectorAssignmentPicker } from "./research-director-assignment-picker";

vi.mock("../../i18n/use-t", () => ({
  useT: () => ({
    t: (selector: (keys: Record<string, unknown>) => unknown) =>
      selector({
        home_overview: { assignments: "Assign" },
        d5: {
          rail: {
            director_role: "Director",
            director_fallback: "Choose a Director",
            director_standby: "Saving",
          },
          inspector: { reason: "Reason" },
        },
      } as never),
  }),
}));

describe("ResearchDirectorAssignmentPicker", () => {
  it("tracks a persisted Director replacement after the picker is already mounted", () => {
    const { rerender } = render(
      <ResearchDirectorAssignmentPicker
        agents={[
          { id: "agent-a", name: "A", display_name: "A", archived_at: null },
          { id: "agent-b", name: "B", display_name: "B", archived_at: null },
        ] as never}
        currentAgentId="agent-a"
        onAssign={vi.fn()}
      />,
    );

    expect(screen.getByRole("combobox")).toHaveValue("agent-a");

    rerender(
      <ResearchDirectorAssignmentPicker
        agents={[
          { id: "agent-a", name: "A", display_name: "A", archived_at: null },
          { id: "agent-b", name: "B", display_name: "B", archived_at: null },
        ] as never}
        currentAgentId="agent-b"
        onAssign={vi.fn()}
      />,
    );

    expect(screen.getByRole("combobox")).toHaveValue("agent-b");
  });
});
