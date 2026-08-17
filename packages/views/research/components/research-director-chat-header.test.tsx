import { render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import enResearch from "../../locales/en/research.json";
import { ResearchDirectorChatHeader } from "./research-director-chat-header";

vi.mock("../../i18n/use-t", () => ({
  useT: () => ({
    t: (selector: (bundle: typeof enResearch) => unknown) => selector(enResearch),
  }),
}));

describe("ResearchDirectorChatHeader", () => {
  it("renders the real lead identity and current activity", () => {
    render(
      <ResearchDirectorChatHeader
        director={{
          id: "member-1",
          agent_id: "agent-1",
          role: "director",
          status: "active",
          is_lead: true,
          display_name: "Ronaldo",
          avatar_url: null,
        }}
        activity="Reviewing integration candidates"
        modeChip={<span>Live</span>}
      />,
    );

    expect(screen.getByText("Ronaldo")).toBeTruthy();
    expect(screen.getByText("Director")).toBeTruthy();
    expect(screen.getByText("Reviewing integration candidates")).toBeTruthy();
    expect(screen.getByText("Live")).toBeTruthy();
  });

  it("degrades to a generic director identity when membership is unavailable", () => {
    render(
      <ResearchDirectorChatHeader
        director={null}
        modeChip={null}
      />,
    );
    expect(screen.getAllByText("Research Director").length).toBeGreaterThan(0);
  });
});
