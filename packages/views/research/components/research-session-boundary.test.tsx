import { useState } from "react";
import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { ResearchSessionBoundary } from "./research-session-boundary";

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
});
