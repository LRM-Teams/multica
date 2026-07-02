import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { I18nProvider } from "@multica/core/i18n/react";
import enCommon from "../../locales/en/common.json";
import enOnboarding from "../../locales/en/onboarding.json";
import { CliInstallInstructions } from "./cli-install-instructions";

const TEST_RESOURCES = { en: { common: enCommon, onboarding: enOnboarding } };

const ligatureClasses = [
  "[font-variant-ligatures:none]",
  "[font-feature-settings:'liga'_0]",
];

describe("CliInstallInstructions", () => {
  it("uses the current repository install script", () => {
    render(
      <I18nProvider locale="en" resources={TEST_RESOURCES}>
        <CliInstallInstructions />
      </I18nProvider>,
    );

    expect(
      screen.getByText(
        "curl -fsSL https://raw.githubusercontent.com/LRM-Teams/multica/main/scripts/install.sh | bash",
      ),
    ).toBeTruthy();
  });

  it("disables font ligatures in CLI command code", () => {
    render(
      <I18nProvider locale="en" resources={TEST_RESOURCES}>
        <CliInstallInstructions />
      </I18nProvider>,
    );

    expect(screen.getByText("multica setup")).toHaveClass(...ligatureClasses);
  });

  it("uses the PowerShell installer for Windows", () => {
    render(
      <I18nProvider locale="en" resources={TEST_RESOURCES}>
        <CliInstallInstructions />
      </I18nProvider>,
    );

    fireEvent.click(screen.getByRole("radio", { name: "Windows" }));

    expect(
      screen.getByText(
        "irm https://raw.githubusercontent.com/LRM-Teams/multica/main/scripts/install.ps1 | iex",
      ),
    ).toBeTruthy();
    expect(screen.getByText("multica setup")).toBeTruthy();
  });

  it("does not render the legacy Windows + WSL mode", () => {
    render(
      <I18nProvider locale="en" resources={TEST_RESOURCES}>
        <CliInstallInstructions />
      </I18nProvider>,
    );

    expect(screen.getByRole("radio", { name: "Mac / Linux" })).toBeTruthy();
    expect(screen.getByRole("radio", { name: "Windows" })).toBeTruthy();
    expect(screen.queryByRole("radio", { name: "Windows + WSL" })).toBeNull();
    expect(screen.queryByText(/callback-host/)).toBeNull();
  });
});
