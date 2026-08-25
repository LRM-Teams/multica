/**
 * @vitest-environment happy-dom
 */
import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { renderWithI18n } from "../test/i18n";
import { NoteSelectionQuotePreview } from "./note-selection-quote-preview";

describe("NoteSelectionQuotePreview", () => {
  it("shows the abbreviated excerpt and dismisses on cancel", async () => {
    const onRemove = vi.fn();
    const user = userEvent.setup();
    renderWithI18n(
      <NoteSelectionQuotePreview
        excerpts={[{ id: "e1", summary: "第一段的缩略引用…" }]}
        onRemove={onRemove}
      />,
      { locale: "zh-Hans" },
    );

    expect(screen.getByTestId("note-selection-quote-preview").textContent).toContain("第一段的缩略引用…");
    await user.click(screen.getByRole("button", { name: "取消引用" }));
    expect(onRemove).toHaveBeenCalledWith("e1");
  });

  it("keeps each excerpt on its own dismissible row", async () => {
    const onRemove = vi.fn();
    const user = userEvent.setup();
    renderWithI18n(
      <NoteSelectionQuotePreview
        excerpts={[
          { id: "e1", summary: "第一段" },
          { id: "e2", summary: "第二段" },
        ]}
        onRemove={onRemove}
      />,
      { locale: "zh-Hans" },
    );

    const rows = screen.getAllByTestId("note-selection-quote-excerpt");
    expect(rows).toHaveLength(2);
    expect(rows[0]?.textContent).toContain("选中内容 1");
    expect(rows[1]?.textContent).toContain("第二段");
    await user.click(screen.getAllByRole("button", { name: "取消引用" })[0]!);
    expect(onRemove).toHaveBeenCalledWith("e1");
    expect(onRemove).not.toHaveBeenCalledWith("e2");
  });
});
