/**
 * @vitest-environment happy-dom
 */
import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { renderWithI18n } from "../test/i18n";
import { NoteIntentEntry } from "./note-intent-entry";

describe("NoteIntentEntry (S3-A4)", () => {
  it("routes to editor, worker, and create_issue", async () => {
    const user = userEvent.setup();
    const onSelect = vi.fn();
    renderWithI18n(<NoteIntentEntry onSelect={onSelect} />);

    const openMenu = async () => {
      await user.click(
        screen.getByRole("button", {
          name: /choose what to do with this note|选择要对这篇笔记做什么/i,
        }),
      );
    };

    await openMenu();
    await user.click(await screen.findByText(/edit this note|改这篇笔记/i));
    expect(onSelect).toHaveBeenCalledWith("editor");

    onSelect.mockClear();
    await openMenu();
    await user.click(await screen.findByText(/work from this note|按这篇做/i));
    expect(onSelect).toHaveBeenCalledWith("worker");

    onSelect.mockClear();
    await openMenu();
    await user.click(await screen.findByText(/create issue|创建 Issue/i));
    expect(onSelect).toHaveBeenCalledWith("create_issue");
  });
});
