import { useState } from "react";
import { fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { ResearchSessionBoundary } from "./research-session-boundary";

type ResearchBoundaryCopy = {
  session_page: {
    load_failed: string;
    load_failed_hint: string;
    technical_details: string;
    retry: string;
  };
};

vi.mock("../../i18n/use-t", () => ({
  useT: () => ({
    t: (selector: (value: ResearchBoundaryCopy) => string) => selector(copy),
  }),
}));

const copy: ResearchBoundaryCopy = {
  session_page: {
    load_failed: "Research could not be displayed",
    load_failed_hint: "Reload the research data and try again.",
    technical_details: "Technical details",
    retry: "Retry",
  },
};

afterEach(() => {
  vi.restoreAllMocks();
});

function DraftProbe() {
  const [draft, setDraft] = useState("");
  return (
    <input
      aria-label="draft"
      value={draft}
      onChange={(event) => setDraft(event.target.value)}
    />
  );
}

describe("ResearchSessionBoundary", () => {
  it("remounts local session state when the session id changes", () => {
    const { rerender } = render(
      <ResearchSessionBoundary sessionId="s1">
        <DraftProbe />
      </ResearchSessionBoundary>,
    );
    fireEvent.change(screen.getByLabelText("draft"), {
      target: { value: "old-session-only" },
    });
    expect(screen.getByLabelText("draft")).toHaveValue("old-session-only");

    rerender(
      <ResearchSessionBoundary sessionId="s2">
        <DraftProbe />
      </ResearchSessionBoundary>,
    );
    expect(screen.getByLabelText("draft")).toHaveValue("");
  });

  it("contains a render failure inside the research route and exposes diagnostics", () => {
    vi.spyOn(console, "error").mockImplementation(() => undefined);

    function BrokenResearchView(): never {
      throw new Error("failed session has no renderable graph");
    }

    render(
      <ResearchSessionBoundary sessionId="failed-session">
        <BrokenResearchView />
      </ResearchSessionBoundary>,
    );

    expect(screen.getByTestId("research-session-render-error")).toBeInTheDocument();
    expect(screen.getByText("Research could not be displayed")).toBeVisible();
    expect(screen.getByText("failed session has no renderable graph")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Retry" })).toBeEnabled();
  });
});
