import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { I18nProvider } from "@multica/core/i18n/react";
import { configStore } from "@multica/core/config";
import enCommon from "../../locales/en/common.json";
import enRuntimes from "../../locales/en/runtimes.json";
import { ConnectRemoteDialog } from "./connect-remote-dialog";

const TEST_RESOURCES = { en: { common: enCommon, runtimes: enRuntimes } };

vi.mock("@multica/core/hooks", () => ({
  useWorkspaceId: () => "ws-test",
}));

vi.mock("@multica/core/paths", () => ({
  paths: {
    workspace: () => ({
      agents: () => "/agents",
      computers: () => "/computers",
    }),
  },
  useWorkspaceSlug: () => "workspace-test",
}));

vi.mock("@multica/core/realtime", () => ({
  useWSEvent: vi.fn(),
}));

vi.mock("../../navigation/context", () => ({
  useNavigation: () => ({ push: vi.fn() }),
}));

function resetConfigStore() {
  configStore.setState({
    cdnDomain: "",
    allowSignup: true,
    googleClientId: "",
    daemonServerUrl: "",
    daemonAppUrl: "",
    workspaceCreationDisabled: false,
  });
}

function renderDialog(config?: {
  daemonServerUrl?: string;
  daemonAppUrl?: string;
}) {
  resetConfigStore();
  if (config) {
    configStore.getState().setDaemonConfig(config);
  }
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      <I18nProvider locale="en" resources={TEST_RESOURCES}>
        <ConnectRemoteDialog onClose={vi.fn()} />
      </I18nProvider>
    </QueryClientProvider>,
  );
}

const ligatureClasses = [
  "[font-variant-ligatures:none]",
  "[font-feature-settings:'liga'_0]",
];

describe("ConnectRemoteDialog", () => {
  it("uses cloud setup commands by default", () => {
    const { baseElement } = renderDialog();

    expect(baseElement).toHaveTextContent(
      "curl -fsSL https://lrm-2-0-release.oss-cn-beijing.aliyuncs.com/releases/install.sh | bash",
    );
    expect(baseElement).toHaveTextContent("multica setup");
    expect(baseElement).not.toHaveTextContent("multica setup self-host");
  });

  it("uses self-host daemon URLs from runtime config", () => {
    const { baseElement } = renderDialog({
      daemonServerUrl: "https://api.example.com/",
      daemonAppUrl: "https://app.example.com/",
    });

    expect(baseElement).toHaveTextContent(
      "multica setup self-host --server-url https://api.example.com --app-url https://app.example.com",
    );
  });

  it("does not render the legacy Windows + WSL mode", () => {
    const { baseElement } = renderDialog();

    expect(screen.getByRole("radio", { name: "Mac / Linux" })).toBeTruthy();
    expect(screen.getByRole("radio", { name: "Windows" })).toBeTruthy();
    expect(screen.queryByRole("radio", { name: "Windows + WSL" })).toBeNull();
    expect(baseElement).not.toHaveTextContent("callback-host");
  });

  it("disables font ligatures in setup command code", () => {
    const { baseElement } = renderDialog();

    const setupCode = Array.from(baseElement.querySelectorAll("code")).find((node) =>
      node.textContent?.includes("multica setup"),
    );

    expect(setupCode).toHaveClass(...ligatureClasses);
  });

  it("disables font ligatures in troubleshooting diagnostic command code", () => {
    const { baseElement } = renderDialog();

    const statusCode = Array.from(baseElement.querySelectorAll("code")).find((node) =>
      node.textContent?.includes("multica daemon status"),
    );

    expect(statusCode).toHaveClass(...ligatureClasses);
  });

  // Distinct from the "can't open a browser" troubleshooting section — this
  // one guards the step-1 install-command failure path added after users hit
  // a blocked install host with no self-service guidance in the dialog.
  it("offers self-service guidance for the install command itself failing", () => {
    const { baseElement } = renderDialog();

    expect(baseElement).toHaveTextContent("Command not working?");
    expect(baseElement).toHaveTextContent(
      "Check your internet connection and try the command again",
    );
    expect(baseElement).toHaveTextContent(
      "it may be blocking outbound access to the install script",
    );
  });

  // Step 2 (`multica setup`) always ends in the device-code login flow,
  // which already prints a link + one-time code confirmable from any
  // device — headless machines never needed a manual token-paste fallback
  // in the first place. This guards against that stale fallback creeping
  // back in.
  it("explains the device-code flow instead of a manual token fallback for headless machines", () => {
    const { baseElement } = renderDialog();

    expect(baseElement).toHaveTextContent("Can't open a browser on that computer?");
    expect(baseElement).toHaveTextContent(
      "Step 2 already handles this",
    );
    expect(baseElement).not.toHaveTextContent("multica config set server_url");
    expect(baseElement).not.toHaveTextContent("multica login --token");
  });
});
