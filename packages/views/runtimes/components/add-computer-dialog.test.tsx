// @vitest-environment jsdom

import { describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen } from "@testing-library/react";
import { I18nProvider } from "@multica/core/i18n/react";
import enCommon from "../../locales/en/common.json";
import enRuntimes from "../../locales/en/runtimes.json";
import { AddComputerDialog } from "./add-computer-dialog";

const TEST_RESOURCES = { en: { common: enCommon, runtimes: enRuntimes } };

function renderChooser(
  onChooseYourComputer = vi.fn(),
  onChooseCloud = vi.fn(),
  onClose = vi.fn(),
) {
  render(
    <I18nProvider locale="en" resources={TEST_RESOURCES}>
      <AddComputerDialog
        onClose={onClose}
        onChooseYourComputer={onChooseYourComputer}
        onChooseCloud={onChooseCloud}
      />
    </I18nProvider>,
  );
  return { onChooseYourComputer, onChooseCloud, onClose };
}

describe("AddComputerDialog — LRM-1141 Step A", () => {
  it("shows Your computer and selectable Cloud computer", () => {
    renderChooser();
    expect(screen.getByText("Your computer")).toBeInTheDocument();
    expect(screen.getByText("Cloud computer")).toBeInTheDocument();
    expect(screen.getByRole("radio", { name: /Cloud computer/i })).not.toBeDisabled();
    expect(
      screen.getByText("Create a Docker container on a connected sandbox node."),
    ).toBeInTheDocument();
    expect(screen.queryByText("Coming soon")).not.toBeInTheDocument();
  });

  it("Next advances to Your computer path by default", () => {
    const { onChooseYourComputer, onChooseCloud } = renderChooser();
    fireEvent.click(screen.getByRole("button", { name: "Next" }));
    expect(onChooseYourComputer).toHaveBeenCalledTimes(1);
    expect(onChooseCloud).not.toHaveBeenCalled();
  });

  it("Next advances to Cloud path when Cloud is selected", () => {
    const { onChooseYourComputer, onChooseCloud } = renderChooser();
    fireEvent.click(screen.getByRole("radio", { name: /Cloud computer/i }));
    fireEvent.click(screen.getByRole("button", { name: "Next" }));
    expect(onChooseCloud).toHaveBeenCalledTimes(1);
    expect(onChooseYourComputer).not.toHaveBeenCalled();
  });

  it("Cancel closes without advancing", () => {
    const { onChooseYourComputer, onChooseCloud, onClose } = renderChooser();
    fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
    expect(onClose).toHaveBeenCalledTimes(1);
    expect(onChooseYourComputer).not.toHaveBeenCalled();
    expect(onChooseCloud).not.toHaveBeenCalled();
  });
});
