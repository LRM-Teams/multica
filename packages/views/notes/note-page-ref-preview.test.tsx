/**
 * @vitest-environment happy-dom
 */
import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { renderWithI18n } from "../test/i18n";
import { NotePageRefPreview } from "./note-page-ref-preview";

describe("NotePageRefPreview", () => {
  it("renders each referenced page title", () => {
    renderWithI18n(
      <NotePageRefPreview
        refs={[
          { pageId: "p1", title: "本周工作" },
          { pageId: "p2", title: "计划" },
        ]}
        onRemove={() => {}}
      />,
      { locale: "zh-Hans" },
    );
    expect(screen.getByText("本周工作")).toBeTruthy();
    expect(screen.getByText("计划")).toBeTruthy();
    expect(screen.getAllByTestId("note-page-ref-chip")).toHaveLength(2);
  });

  it("removes a chip by page id", async () => {
    const user = userEvent.setup();
    const onRemove = vi.fn();
    renderWithI18n(
      <NotePageRefPreview
        refs={[{ pageId: "p1", title: "本周工作" }]}
        onRemove={onRemove}
      />,
      { locale: "zh-Hans" },
    );
    await user.click(screen.getByRole("button", { name: "取消引用" }));
    expect(onRemove).toHaveBeenCalledWith("p1");
  });
});
