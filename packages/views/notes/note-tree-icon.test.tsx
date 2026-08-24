/**
 * @vitest-environment happy-dom
 */
import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { renderWithI18n } from "../test/i18n";
import { NoteTreeIcon } from "./note-tree-icon";

vi.mock("@multica/ui/components/common/emoji-picker", () => ({
  EmojiPicker: ({ onSelect }: { onSelect: (emoji: string) => void }) => (
    <button type="button" onClick={() => onSelect("📌")}>
      pick-pin
    </button>
  ),
}));

describe("NoteTreeIcon", () => {
  it("shows the stored emoji and lets an owner change it", async () => {
    const user = userEvent.setup();
    const onChange = vi.fn();
    renderWithI18n(<NoteTreeIcon icon="📝" canManage onChange={onChange} />);

    expect(screen.getByRole("button", { name: "Change icon" })).toHaveTextContent("📝");
    await user.click(screen.getByRole("button", { name: "Change icon" }));
    await user.click(screen.getByRole("button", { name: "pick-pin" }));
    expect(onChange).toHaveBeenCalledWith("📌");
  });

  it("renders a static mark for viewers who cannot manage the page", () => {
    renderWithI18n(<NoteTreeIcon icon="📌" canManage={false} onChange={vi.fn()} />);

    expect(screen.queryByRole("button", { name: "Change icon" })).toBeNull();
    expect(screen.getByText("📌")).toBeTruthy();
  });
});
