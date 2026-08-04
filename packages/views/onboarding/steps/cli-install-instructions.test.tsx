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
  it("uses the CDN install script", () => {
    render(
      <I18nProvider locale="en" resources={TEST_RESOURCES}>
        <CliInstallInstructions />
      </I18nProvider>,
    );

    expect(
      screen.getByText(
        "curl -fsSL https://cdn.leagent.me/computer/install.sh | bash",
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

  it("uses the CDN PowerShell installer for Windows", () => {
    render(
      <I18nProvider locale="en" resources={TEST_RESOURCES}>
        <CliInstallInstructions />
      </I18nProvider>,
    );

    fireEvent.click(screen.getByRole("radio", { name: "Windows" }));

    expect(
      screen.getByText(
        "irm https://cdn.leagent.me/computer/install.ps1 | iex",
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

  it("offers a troubleshooting disclosure with self-diagnosis steps, no invented contact", () => {
    render(
      <I18nProvider locale="en" resources={TEST_RESOURCES}>
        <CliInstallInstructions />
      </I18nProvider>,
    );

    // Collapsed by default — content is present in the DOM (native <details>)
    // but the disclosure itself must be there to find.
    expect(
      screen.getByText("Having trouble?"),
    ).toBeTruthy();
    expect(
      screen.getByText(/Retry — a flaky connection/),
    ).toBeTruthy();
    expect(screen.getByText("multica daemon status")).toBeTruthy();
    expect(screen.getByText("multica daemon logs -f")).toBeTruthy();

    // No support email/Discord/etc. exists for this product yet — the
    // section must not fabricate one.
    expect(screen.queryByText(/contact support/i)).toBeNull();
    expect(screen.queryByRole("link")).toBeNull();
  });
});
