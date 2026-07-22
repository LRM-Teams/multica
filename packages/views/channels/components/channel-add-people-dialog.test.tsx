import { render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { ChannelAddPeopleDialog } from "./channel-add-people-dialog";

vi.mock("../../i18n/use-t", () => ({
  useT: () => ({
    t: (selector: (r: Record<string, unknown>) => string) => {
      const resources = {
        members: {
          add_people_title: "Add people to {{name}}",
          add_people_subtitle: "Invite teammates",
          search: "Search",
          suggestions: "Suggestions",
          no_results: "No results",
          no_candidates: "No candidates",
          remove_aria: "Remove",
          cancel: "Cancel",
          add: "Add",
        },
        profile_popover: {
          role: { agent: "Agent" },
        },
      };
      const raw = selector(resources as never);
      return typeof raw === "string" ? raw.replace("{{name}}", "general") : String(raw);
    },
  }),
}));

vi.mock("../../common/actor-avatar", () => ({
  ActorAvatar: () => <span data-testid="actor-avatar" />,
}));

describe("ChannelAddPeopleDialog (LRM-232)", () => {
  it("does not append Agent type text to invite rows", () => {
    render(
      <ChannelAddPeopleDialog
        open
        onOpenChange={() => {}}
        channelName="general"
        candidates={[
          {
            key: "agent:a1",
            type: "agent",
            id: "a1",
            presentation: {
              displayName: "前端工程师",
              handle: "qian-duan",
              handleLabel: "@qian-duan",
              showHandleLabel: true,
            },
          },
        ]}
        allCandidates={[]}
        query=""
        onQueryChange={() => {}}
        selected={new Set()}
        onToggle={() => {}}
        onClearOne={() => {}}
        onSubmit={() => {}}
      />,
    );

    expect(screen.getByText("前端工程师")).toBeInTheDocument();
    expect(screen.getByText("@qian-duan")).toBeInTheDocument();
    expect(screen.queryByText(/Agent/)).not.toBeInTheDocument();
  });
});
