import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import {
  COMPOSER_MENTION_HINT_LS_KEY,
  ComposerMentionHint,
} from "./composer-mention-hint";

vi.mock("../../i18n/use-t", () => ({
  useT: () => ({
    t: (selector: (resources: {
      composer: { mention_hint: string; mention_hint_dismiss: string };
    }) => string) =>
      selector({
        composer: {
          mention_hint: "Type @agent to trigger a reply.",
          mention_hint_dismiss: "Got it",
        },
      }),
  }),
}));

describe("ComposerMentionHint (LRM-491)", () => {
  beforeEach(() => {
    window.localStorage.clear();
  });

  it("shows the @agent tip once and dismisses permanently", async () => {
    const user = userEvent.setup();
    const { unmount } = render(<ComposerMentionHint />);

    expect(
      await screen.findByText("Type @agent to trigger a reply."),
    ).toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "Got it" }));
    expect(screen.queryByText("Type @agent to trigger a reply.")).toBeNull();
    expect(window.localStorage.getItem(COMPOSER_MENTION_HINT_LS_KEY)).toBe("true");

    unmount();
    render(<ComposerMentionHint />);
    expect(screen.queryByText("Type @agent to trigger a reply.")).toBeNull();
  });

  it("stays hidden when already dismissed", () => {
    window.localStorage.setItem(COMPOSER_MENTION_HINT_LS_KEY, "true");
    render(<ComposerMentionHint />);
    expect(screen.queryByText("Type @agent to trigger a reply.")).toBeNull();
  });
});
