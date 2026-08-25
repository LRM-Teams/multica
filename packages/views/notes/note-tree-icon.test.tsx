/**
 * @vitest-environment happy-dom
 */
import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { renderWithI18n } from "../test/i18n";
import { NOTE_TREE_ICON_PRESETS, NoteTreeIcon } from "./note-tree-icon";

vi.mock("@multica/ui/components/common/emoji-picker", () => ({
  EmojiPicker: ({ onSelect }: { onSelect: (emoji: string) => void }) => (
    <button type="button" onClick={() => onSelect("🌈")}>
      pick-full
    </button>
  ),
}));

describe("NoteTreeIcon", () => {
  it("opens a preset grid on the same click, without mounting emoji-mart", async () => {
    const user = userEvent.setup();
    const onChange = vi.fn();
    renderWithI18n(<NoteTreeIcon icon="📝" canManage onChange={onChange} />);

    await user.click(screen.getByRole("button", { name: "Change icon" }));

    expect(screen.getByRole("button", { name: NOTE_TREE_ICON_PRESETS[0] })).toBeTruthy();
    expect(screen.queryByRole("button", { name: "pick-full" })).toBeNull();

    await user.click(screen.getByRole("button", { name: "📌" }));
    expect(onChange).toHaveBeenCalledWith("📌");
  });

  it("loads the full gallery only after More emojis", async () => {
    const user = userEvent.setup();
    const onChange = vi.fn();
    renderWithI18n(<NoteTreeIcon canManage onChange={onChange} />);

    await user.click(screen.getByRole("button", { name: "Change icon" }));
    await user.click(screen.getByRole("button", { name: "More emojis" }));
    await user.click(await screen.findByRole("button", { name: "pick-full" }));
    expect(onChange).toHaveBeenCalledWith("🌈");
  });

  it("renders a static mark for viewers who cannot manage the page", () => {
    renderWithI18n(<NoteTreeIcon icon="📌" canManage={false} onChange={vi.fn()} />);

    expect(screen.queryByRole("button", { name: "Change icon" })).toBeNull();
    expect(screen.getByText("📌")).toBeTruthy();
  });
});
