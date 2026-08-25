import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import type { ResearchSelectedReference } from "@multica/core/types";
import { ResearchSelectedRefChip } from "./research-selected-ref-chip";

vi.mock("../../i18n/use-t", () => ({
  useT: () => ({
    t: (
      fn: (dict: Record<string, unknown>) => unknown,
      vars?: Record<string, unknown>,
    ) => {
      const value = fn({
        panel: {
          selected_ref_revision: "revision {{revision}}",
          selected_ref_remove: "Remove reference: {{summary}}",
        },
      });
      return typeof value === "string"
        ? value.replace(/\{\{(\w+)\}\}/g, (_, key: string) => String(vars?.[key] ?? ""))
        : value;
    },
  }),
}));

const reference: ResearchSelectedReference = {
  stableId: "insight:00000000-0000-4000-8000-000000000306",
  kind: "insight",
  entityId: "00000000-0000-4000-8000-000000000306",
  revision: 2,
  contentHash: `sha256:${"c".repeat(64)}`,
  displaySummary: "Latency boundary under concurrent load",
};

describe("ResearchSelectedRefChip", () => {
  it("shows the exact reference and removes it by stable id", () => {
    const onRemove = vi.fn();
    render(
      <ResearchSelectedRefChip reference={reference} onRemove={onRemove} />,
    );

    expect(screen.getByText(reference.displaySummary)).toBeTruthy();
    expect(screen.getByText("revision 2")).toBeTruthy();
    fireEvent.click(
      screen.getByRole("button", {
        name: `Remove reference: ${reference.displaySummary}`,
      }),
    );
    expect(onRemove).toHaveBeenCalledWith(reference.stableId);
  });
});
