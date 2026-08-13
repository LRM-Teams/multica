import { useState } from "react";
import { fireEvent, render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { ResearchSessionBoundary } from "./research-session-boundary";

const mockResearchCopy = {
  session_page: {
    load_failed: "Could not load research",
    load_failed_hint: "Existing research is unaffected.",
    retry: "Retry",
  },
};

vi.mock("../../i18n/use-t", () => ({
  useT: () => ({
    t: (selector: (copy: typeof mockResearchCopy) => string) =>
      selector(mockResearchCopy),
  }),
}));

let shouldThrow = false;

function RenderProbe() {
  if (shouldThrow) throw new Error("incomplete failed-session projection");
  return <p>Research canvas</p>;
}

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
  beforeEach(() => {
    shouldThrow = false;
  });

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

  it("contains a failed-session render exception instead of crashing the page", () => {
    const consoleError = vi.spyOn(console, "error").mockImplementation(() => undefined);
    shouldThrow = true;

    render(
      <ResearchSessionBoundary sessionId="failed-session">
        <RenderProbe />
      </ResearchSessionBoundary>,
    );

    expect(screen.getByTestId("research-session-render-error")).toHaveTextContent(
      "Could not load research",
    );
    expect(consoleError).toHaveBeenCalledWith(
      "Research session render failed",
      expect.objectContaining({ sessionId: "failed-session" }),
    );

    shouldThrow = false;
    fireEvent.click(screen.getByRole("button", { name: "Retry" }));
    expect(screen.getByText("Research canvas")).toBeInTheDocument();
    consoleError.mockRestore();
  });
});
