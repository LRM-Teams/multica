// @vitest-environment jsdom

import { describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen } from "@testing-library/react";
import { I18nProvider } from "@multica/core/i18n/react";
import enCommon from "../../locales/en/common.json";
import enRuntimes from "../../locales/en/runtimes.json";
import { AddComputerDialog } from "./add-computer-dialog";

const TEST_RESOURCES = { en: { common: enCommon, runtimes: enRuntimes } };

function renderChooser(onChooseYourComputer = vi.fn(), onClose = vi.fn()) {
  render(
    <I18nProvider locale="en" resources={TEST_RESOURCES}>
      <AddComputerDialog
        onClose={onClose}
        onChooseYourComputer={onChooseYourComputer}
      />
    </I18nProvider>,
  );
  return { onChooseYourComputer, onClose };
}

describe("AddComputerDialog — LRM-1094 Step A", () => {
  it("shows Your computer and disabled Cloud Coming soon", () => {
    renderChooser();
    expect(screen.getByText("Your computer")).toBeInTheDocument();
    expect(screen.getByText("Cloud computer")).toBeInTheDocument();
    expect(screen.getByRole("radio", { name: /Cloud computer/i })).toBeDisabled();
    expect(screen.getAllByText("Coming soon").length).toBeGreaterThan(0);
  });

  it("Next advances to Your computer path", () => {
    const { onChooseYourComputer } = renderChooser();
    fireEvent.click(screen.getByRole("button", { name: "Next" }));
    expect(onChooseYourComputer).toHaveBeenCalledTimes(1);
  });

  it("Cancel closes without advancing", () => {
    const { onChooseYourComputer, onClose } = renderChooser();
    fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
    expect(onClose).toHaveBeenCalledTimes(1);
    expect(onChooseYourComputer).not.toHaveBeenCalled();
  });
});
