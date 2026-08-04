// @vitest-environment jsdom
import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { I18nProvider } from "@multica/core/i18n/react";
import type { AgentRuntime } from "@multica/core/types";
import enRuntimes from "../../locales/en/runtimes.json";
import { MachineDaemonUpgrade } from "./machine-daemon-upgrade";

const initiateUpdate = vi.hoisted(() => vi.fn());
const getUpdateResult = vi.hoisted(() => vi.fn());

vi.mock("@multica/core/api", async (importActual) => {
  const actual = await importActual<typeof import("@multica/core/api")>();
  return {
    ...actual,
    api: {
      ...actual.api,
      initiateUpdate: (...args: unknown[]) => initiateUpdate(...args),
      getUpdateResult: (...args: unknown[]) => getUpdateResult(...args),
    },
  };
});

function makeRuntime(over: Partial<AgentRuntime> = {}): AgentRuntime {
  return {
    id: "rt-1",
    workspace_id: "ws-1",
    name: "daemon",
    status: "online",
    runtime_health: "update_available",
    update_state: "idle",
    current_version: "0.3.99",
    target_version: "0.4.0",
    update_error: null,
    ...over,
  } as AgentRuntime;
}

function wrap(ui: React.ReactElement) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      <I18nProvider locale="en" resources={{ en: { runtimes: enRuntimes } }}>
        {ui}
      </I18nProvider>
    </QueryClientProvider>,
  );
}

beforeEach(() => {
  vi.clearAllMocks();
  initiateUpdate.mockResolvedValue({ id: "upd-1" });
  getUpdateResult.mockResolvedValue({ status: "running" });
});

describe("MachineDaemonUpgrade (#29)", () => {
  it("idle: shows version and upgrade CTA with target", () => {
    const rt = makeRuntime();
    wrap(
      <MachineDaemonUpgrade
        runtime={rt}
        cliVersion="0.3.99"
        updateTargetVersion="0.4.0"
        updateError={null}
        isOnline
        canUpdate
      />,
    );
    expect(screen.getByTestId("machine-basics-daemon-version")).toHaveTextContent(
      "0.3.99",
    );
    expect(screen.getByTestId("machine-daemon-upgrade-btn")).toHaveTextContent(
      /Upgrade to 0\.4\.0/,
    );
    expect(screen.queryByTestId("machine-daemon-upgrade-progress")).toBeNull();
  });

  it("equal target does not show upgrade CTA (P0 IsNewer gate)", () => {
    const rt = makeRuntime({
      runtime_health: "update_available",
      target_version: "0.4.0",
    });
    wrap(
      <MachineDaemonUpgrade
        runtime={rt}
        cliVersion="0.4.0"
        updateTargetVersion="0.4.0"
        updateError={null}
        isOnline
        canUpdate
      />,
    );
    expect(screen.getByTestId("machine-basics-daemon-version")).toHaveTextContent(
      "0.4.0",
    );
    expect(screen.queryByTestId("machine-daemon-upgrade-btn")).toBeNull();
  });

  it("click upgrade: shows current → target + progress, no grey CTA", async () => {
    const rt = makeRuntime();
    wrap(
      <MachineDaemonUpgrade
        runtime={rt}
        cliVersion="0.3.99"
        updateTargetVersion="0.4.0"
        updateError={null}
        isOnline
        canUpdate
      />,
    );
    fireEvent.click(screen.getByTestId("machine-daemon-upgrade-btn"));
    await waitFor(() => {
      expect(screen.getByTestId("machine-daemon-upgrade")).toHaveAttribute(
        "data-state",
        "active",
      );
    });
    expect(screen.getByTestId("machine-basics-daemon-target")).toHaveTextContent(
      "0.4.0",
    );
    expect(screen.getByTestId("machine-daemon-upgrade-progress")).toHaveTextContent(
      /Starting update|Downloading/,
    );
    // No disabled outline "Updating…" button
    expect(screen.queryByTestId("machine-daemon-upgrade-btn")).toBeNull();
    expect(initiateUpdate).toHaveBeenCalledWith("rt-1", "0.4.0");
  });

  it("non-owner: no upgrade CTA when update available", () => {
    const rt = makeRuntime();
    wrap(
      <MachineDaemonUpgrade
        runtime={rt}
        cliVersion="0.3.99"
        updateTargetVersion="0.4.0"
        updateError={null}
        isOnline
        canUpdate={false}
      />,
    );
    expect(screen.queryByTestId("machine-daemon-upgrade-btn")).toBeNull();
    expect(screen.getByTestId("machine-daemon-upgrade")).toHaveAttribute(
      "data-state",
      "owner-only",
    );
    expect(screen.getByText(/Only the computer owner/i)).toBeInTheDocument();
  });

  it("failed: keeps → target, human reason, and Retry (even when health is failed)", () => {
    const rt = makeRuntime({
      runtime_health: "failed",
      update_state: "failed",
      update_error: "download_timeout",
      target_version: "0.4.0",
    });
    wrap(
      <MachineDaemonUpgrade
        runtime={rt}
        cliVersion="0.3.99"
        updateTargetVersion="0.4.0"
        updateError="download_timeout"
        isOnline
        canUpdate
      />,
    );
    expect(screen.getByTestId("machine-daemon-upgrade")).toHaveAttribute(
      "data-state",
      "failed",
    );
    expect(screen.getByTestId("machine-basics-daemon-target")).toHaveTextContent(
      "0.4.0",
    );
    expect(screen.getByTestId("machine-daemon-upgrade-error").textContent).toMatch(
      /Upgrade failed/i,
    );
    expect(screen.getByTestId("machine-daemon-upgrade-fail")).toBeEnabled();
  });

  it("poll completed + version caught up clears applying spinner (v-prefix OK)", async () => {
    getUpdateResult.mockResolvedValue({ status: "completed" });
    vi.spyOn(globalThis, "setInterval").mockImplementation(((
      fn: TimerHandler,
      // eslint-disable-next-line @typescript-eslint/no-unused-vars
      _timeout?: number,
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
      ..._args: any[]
    ) => {
      if (typeof fn === "function") {
        queueMicrotask(() => {
          (fn as () => void)();
        });
      }
      return 1 as unknown as ReturnType<typeof setInterval>;
    }) as unknown as typeof setInterval);
    vi.spyOn(globalThis, "clearInterval").mockImplementation(() => {});
    const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    const tree = (node: React.ReactElement) => (
      <QueryClientProvider client={qc}>
        <I18nProvider locale="en" resources={{ en: { runtimes: enRuntimes } }}>
          {node}
        </I18nProvider>
      </QueryClientProvider>
    );
    const { rerender } = render(
      tree(
        <MachineDaemonUpgrade
          runtime={makeRuntime({
            current_version: "0.4.1",
            target_version: "v0.4.2",
          })}
          cliVersion="0.4.1"
          updateTargetVersion="v0.4.2"
          updateError={null}
          isOnline
          canUpdate
        />,
      ),
    );
    fireEvent.click(screen.getByTestId("machine-daemon-upgrade-btn"));
    await waitFor(() => {
      expect(screen.getByTestId("machine-daemon-upgrade-progress")).toHaveTextContent(
        /Restarting|switching/i,
      );
    });

    // Daemon back with equal semver (prefix mismatch like Frank's 0.4.2 → v0.4.2).
    rerender(
      tree(
        <MachineDaemonUpgrade
          runtime={makeRuntime({
            runtime_health: "ok",
            update_state: "completed",
            current_version: "0.4.2",
            target_version: "v0.4.2",
          })}
          cliVersion="0.4.2"
          updateTargetVersion="v0.4.2"
          updateError={null}
          isOnline
          canUpdate
        />,
      ),
    );
    await waitFor(() => {
      expect(screen.queryByTestId("machine-daemon-upgrade-progress")).toBeNull();
    });
    expect(screen.getByTestId("machine-basics-daemon-version")).toHaveTextContent(
      "0.4.2",
    );
    expect(screen.getByTestId("machine-daemon-upgrade")).not.toHaveAttribute(
      "data-state",
      "active",
    );
  });
});
