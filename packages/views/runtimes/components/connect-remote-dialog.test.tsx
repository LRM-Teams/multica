import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { I18nProvider } from "@multica/core/i18n/react";
import { configStore } from "@multica/core/config";
import enCommon from "../../locales/en/common.json";
import enRuntimes from "../../locales/en/runtimes.json";
import { ConnectRemoteDialog } from "./connect-remote-dialog";

const TEST_RESOURCES = { en: { common: enCommon, runtimes: enRuntimes } };

const wsHandlers = new Map<string, (payload: unknown) => void>();

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
  useWSEvent: (event: string, handler: (payload: unknown) => void) => {
    wsHandlers.set(event, handler);
  },
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
  beforeEach(() => {
    wsHandlers.clear();
  });

  it("uses cloud setup commands by default", () => {
    const { baseElement } = renderDialog();

    expect(baseElement).toHaveTextContent(
      "curl -fsSL https://cdn.leagent.me/computer/install.sh | bash",
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

  // LRM-1176 / LRM-1129 freeze ① — install + browser trouble share one
  // "Having trouble?" details after Waiting; both bodies stay intact.
  it("merges install and browser trouble into one details after Waiting", () => {
    const { baseElement } = renderDialog();

    expect(baseElement).toHaveTextContent("Having trouble?");
    expect(baseElement).toHaveTextContent("Command not working?");
    expect(baseElement).toHaveTextContent(
      "Check your internet connection and try the command again",
    );
    expect(baseElement).toHaveTextContent(
      "it may be blocking outbound access to the install script",
    );
    expect(baseElement).toHaveTextContent("Can't open a browser on that computer?");
    expect(baseElement).toHaveTextContent("Step 2 already handles this");
    expect(baseElement).not.toHaveTextContent("multica config set server_url");
    expect(baseElement).not.toHaveTextContent("multica login --token");

    const details = baseElement.querySelectorAll("details");
    expect(details).toHaveLength(1);
    // LRM-1199: expandable help uses solid border vocabulary, not dropzone dashed.
    expect(details[0]).toHaveClass("border", "border-border");
    expect(details[0]).not.toHaveClass("border-dashed");
    const waiting = screen.getByRole("status");
    expect(
      waiting.compareDocumentPosition(details[0]!) &
        Node.DOCUMENT_POSITION_FOLLOWING,
    ).toBeTruthy();
  });

  it("keeps OS selector compact without visible mode label or hints", () => {
    const { baseElement } = renderDialog();

    expect(baseElement).toHaveTextContent("Install commands");
    expect(baseElement).not.toHaveTextContent("Where are you running this?");
    expect(baseElement).not.toHaveTextContent(
      "Use this for macOS, Linux, SSH boxes",
    );
    expect(
      screen.getByRole("radiogroup", { name: "Where are you running this?" }),
    ).toBeTruthy();
  });

  // LRM-1141 / LRM-1129 freeze v2 — Waiting is brand-soft status; Done disabled
  // until daemon:register replaces the panel with the existing success state.
  it("shows Waiting status and disabled Done before register", () => {
    renderDialog();

    const status = screen.getByRole("status");
    expect(status).toHaveTextContent("Waiting for computer to connect");
    expect(status).toHaveTextContent(
      "Keep this dialog open. It will continue automatically when the computer is registered.",
    );
    expect(status.className).toContain("bg-brand/5");
    expect(status.className).not.toContain("bg-success");
    expect(screen.getByRole("button", { name: "Done" })).toBeDisabled();
  });

  it("replaces Waiting with success after daemon:register", () => {
    renderDialog();

    act(() => {
      wsHandlers.get("daemon:register")!({ runtime_id: "rt-1" });
    });

    expect(screen.getByText("Computer connected")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Done" })).toBeNull();
    expect(screen.getByRole("button", { name: /Create an agent/i })).toBeInTheDocument();
  });
});
