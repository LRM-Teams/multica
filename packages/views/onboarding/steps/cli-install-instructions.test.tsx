import { fireEvent, render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it } from "vitest";
import { I18nProvider } from "@multica/core/i18n/react";
import { configStore } from "@multica/core/config";
import enCommon from "../../locales/en/common.json";
import enOnboarding from "../../locales/en/onboarding.json";
import { CliInstallInstructions } from "./cli-install-instructions";

const TEST_RESOURCES = { en: { common: enCommon, onboarding: enOnboarding } };

const ligatureClasses = [
  "[font-variant-ligatures:none]",
  "[font-feature-settings:'liga'_0]",
];

describe("CliInstallInstructions", () => {
  beforeEach(() => {
    configStore.getState().setDaemonConfig({
      environment: "production",
      daemonServerUrl: "https://api.leagent.me",
      daemonAppUrl: "https://www.leagent.me",
    });
  });

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

    expect(
      screen.getByText("multica setup /<workspace-slug>"),
    ).toHaveClass(...ligatureClasses);
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
    expect(
      screen.getByText("multica setup /<workspace-slug>"),
    ).toBeTruthy();
  });

  it("uses the deployment-pinned installer and explicit endpoints in test", () => {
    configStore.getState().setDaemonConfig({
      environment: "test",
      daemonServerUrl: "https://82.157.184.89/",
      daemonAppUrl: "https://82.157.184.89/",
      computerVersion: "v0.4.24-alpha.2",
    });

    render(
      <I18nProvider locale="en" resources={TEST_RESOURCES}>
        <CliInstallInstructions workspaceSlug="lrm-team-test" />
      </I18nProvider>,
    );

    expect(
      screen.getByText(
        "curl -fsSL https://cdn.leagent.me/computer/install.sh | bash -s -- --version v0.4.24-alpha.2",
      ),
    ).toBeTruthy();
    expect(
      screen.getByText(
        "multica setup --environment test --server-url https://82.157.184.89 --app-url https://82.157.184.89 /lrm-team-test",
      ),
    ).toBeTruthy();
  });

  it("uses the deployment-pinned PowerShell installer in test", () => {
    configStore.getState().setDaemonConfig({
      environment: "test",
      computerVersion: "v0.4.24-alpha.2",
    });

    render(
      <I18nProvider locale="en" resources={TEST_RESOURCES}>
        <CliInstallInstructions />
      </I18nProvider>,
    );
    fireEvent.click(screen.getByRole("radio", { name: "Windows" }));

    expect(
      screen.getByText(
        "& ([scriptblock]::Create((irm https://cdn.leagent.me/computer/install.ps1))) -Version v0.4.24-alpha.2",
      ),
    ).toBeTruthy();
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

  it("renders workspace-scoped setup command (not bare multica setup)", () => {
    render(
      <I18nProvider locale="en" resources={TEST_RESOURCES}>
        <CliInstallInstructions />
      </I18nProvider>,
    );

    // The setup command must include a workspace slug path — bare `multica setup`
    // without a workspace scope is no longer the expected flow (LRM-1420).
    expect(screen.queryByText("multica setup")).toBeNull();
    expect(
      screen.getByText(/^multica setup \/</),
    ).toBeTruthy();
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
    expect(screen.getByText("multica computer status")).toBeTruthy();
    expect(screen.getByText("multica computer logs -f")).toBeTruthy();

    // No support email/Discord/etc. exists for this product yet — the
    // section must not fabricate one.
    expect(screen.queryByText(/contact support/i)).toBeNull();
    expect(screen.queryByRole("link")).toBeNull();
  });
});
