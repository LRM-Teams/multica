import { describe, expect, it } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import { QuickEmojiPicker } from "@multica/ui/components/common/quick-emoji-picker";

/**
 * LRM-1375 — the shared emoji picker must not hardcode English copy.
 *
 * The full-gallery toggle previously rendered a hardcoded `More emojis...`
 * and the lazy fallback rendered a hardcoded `Loading...`, leaking English on
 * every non-English locale and giving the loading state no `role=status`.
 *
 * Picker text is now caller-provided so the channels and issues surfaces can
 * pass their own i18n strings; `packages/ui` stays free of business i18n.
 */
describe("QuickEmojiPicker — caller-provided copy (LRM-1375)", () => {
  const onSelect = () => {};

  it("renders the caller's 'More emojis' label, not the hardcoded English", () => {
    render(
      <QuickEmojiPicker onSelect={onSelect} moreLabel="更多表情" />,
    );
    fireEvent.click(screen.getByRole("button", { name: "Add reaction" }));
    // The quick grid renders emoji buttons that carry the emoji as their
    // accessible name, so the toggle is the only control with this text.
    expect(
      screen.getByRole("button", { name: "更多表情" }),
    ).toBeInTheDocument();
  });

  it("gives every quick emoji a real accessible name", () => {
    render(
      <QuickEmojiPicker onSelect={onSelect} emojis={["👍", "👌"]} />,
    );
    fireEvent.click(screen.getByRole("button", { name: "Add reaction" }));
    expect(screen.getByRole("button", { name: "👍" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "👌" })).toBeInTheDocument();
  });

  it("falls back to a sensible default 'More emojis' when not provided", () => {
    render(<QuickEmojiPicker onSelect={onSelect} />);
    fireEvent.click(screen.getByRole("button", { name: "Add reaction" }));
    expect(
      screen.getByRole("button", { name: "More emojis" }),
    ).toBeInTheDocument();
  });
});
