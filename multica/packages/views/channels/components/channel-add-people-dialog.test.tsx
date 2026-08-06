import { act, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { ChannelAddPeopleDialog, type InviteCandidate } from "./channel-add-people-dialog";

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
          candidates_error: "Couldn't load people to invite",
          candidates_retry: "Retry",
          candidates_slow: "Still loading…",
          candidates_timeout: "Loading is taking too long",
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

const emptyCandidates: InviteCandidate[] = [];

const baseProps = {
  open: true,
  onOpenChange: () => {},
  channelName: "general",
  candidates: emptyCandidates,
  allCandidates: emptyCandidates,
  query: "",
  onQueryChange: () => {},
  selected: new Set<string>(),
  onToggle: () => {},
  onClearOne: () => {},
  onSubmit: () => {},
};

describe("ChannelAddPeopleDialog (LRM-232 / LRM-623)", () => {
  beforeEach(() => {
    vi.useFakeTimers();
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  it("does not append Agent type text to invite rows", () => {
    render(
      <ChannelAddPeopleDialog
        {...baseProps}
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
      />,
    );

    expect(screen.getByText("前端工程师")).toBeInTheDocument();
    expect(screen.getByText("@qian-duan")).toBeInTheDocument();
    expect(screen.queryByText(/Agent/)).not.toBeInTheDocument();
  });

  it("shows first-screen skeleton while loading (not silent empty)", () => {
    render(<ChannelAddPeopleDialog {...baseProps} loading />);

    expect(screen.getByTestId("add-people-loading")).toBeInTheDocument();
    expect(screen.queryByTestId("add-people-empty")).not.toBeInTheDocument();
    expect(screen.queryByText("No candidates")).not.toBeInTheDocument();
  });

  it("shows slow hint after 2s while still loading", () => {
    render(<ChannelAddPeopleDialog {...baseProps} loading />);

    expect(screen.queryByText("Still loading…")).not.toBeInTheDocument();
    act(() => {
      vi.advanceTimersByTime(2_000);
    });
    expect(screen.getByText("Still loading…")).toBeInTheDocument();
  });

  it("shows explicit timeout with retry instead of empty list", () => {
    const onRetry = vi.fn();
    render(<ChannelAddPeopleDialog {...baseProps} loading onRetry={onRetry} />);

    act(() => {
      vi.advanceTimersByTime(8_000);
    });

    expect(screen.getByTestId("add-people-error")).toBeInTheDocument();
    expect(screen.getByText("Loading is taking too long")).toBeInTheDocument();
    expect(screen.queryByTestId("add-people-empty")).not.toBeInTheDocument();
    screen.getByRole("button", { name: "Retry" }).click();
    expect(onRetry).toHaveBeenCalledTimes(1);
  });

  it("shows fetch error with retry (LRM-238: no silent empty)", () => {
    const onRetry = vi.fn();
    render(<ChannelAddPeopleDialog {...baseProps} error onRetry={onRetry} />);

    expect(screen.getByText("Couldn't load people to invite")).toBeInTheDocument();
    expect(screen.queryByText("No candidates")).not.toBeInTheDocument();
    screen.getByRole("button", { name: "Retry" }).click();
    expect(onRetry).toHaveBeenCalledTimes(1);
  });

  it("shows empty only after successful load with no candidates", () => {
    render(<ChannelAddPeopleDialog {...baseProps} />);

    expect(screen.getByTestId("add-people-empty")).toBeInTheDocument();
    expect(screen.getByText("No candidates")).toBeInTheDocument();
  });
});
