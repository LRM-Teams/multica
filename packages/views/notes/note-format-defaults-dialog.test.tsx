/**
 * @vitest-environment happy-dom
 */
import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it } from "vitest";
import { DEFAULT_NOTE_FORMAT } from "@multica/core/notes/format";
import { useNoteFormatStore } from "@multica/core/notes/format-store";
import { renderWithI18n } from "../test/i18n";
import { NoteFormatDefaultsDialog } from "./note-format-defaults-dialog";

describe("NoteFormatDefaultsDialog", () => {
  beforeEach(() => {
    useNoteFormatStore.setState({ ...DEFAULT_NOTE_FORMAT });
  });

  it("writes the chosen defaults into the format store", async () => {
    const user = userEvent.setup();
    renderWithI18n(
      <NoteFormatDefaultsDialog open onOpenChange={() => {}} />,
      { locale: "zh-Hans" },
    );

    expect(screen.getByRole("heading", { name: "默认格式" })).toBeTruthy();
    await user.click(screen.getByRole("button", { name: "红" }));
    expect(useNoteFormatStore.getState().color).toBe("red");

    await user.click(screen.getByRole("button", { name: "重置" }));
    expect(useNoteFormatStore.getState()).toMatchObject(DEFAULT_NOTE_FORMAT);
  });
});
