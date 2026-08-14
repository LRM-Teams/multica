// @vitest-environment jsdom

import { fireEvent, render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

const restart = vi.hoisted(() => ({
  mutate: vi.fn(),
  clear: vi.fn(),
  refreshAfterRequest: vi.fn(),
  isPending: false,
  isError: false,
}));

vi.mock("@multica/core/agents", () => ({
  useAgentRestart: () => ({
    preflight: {
      actions: {
        restart: { supported: true },
        session: { supported: true },
        full: { supported: true },
      },
    },
    resetAgent: {
      mutate: (...args: unknown[]) => restart.mutate(...args),
      isPending: restart.isPending,
      isError: restart.isError,
    },
    clear: restart.clear,
    refreshAfterRequest: restart.refreshAfterRequest,
  }),
  agentRestartModeState: (
    preflight: { actions?: Record<string, { supported: boolean }> } | null,
    mode: string,
  ) => preflight?.actions?.[mode] ?? { supported: false },
  resolveRestartDisabledReasonKey: () => "unavailable",
}));

const RESOURCES = {
  restart_modal: {
    title: "Restart agent",
    description_short: "Restart {{name}}.",
    recommended: "Default",
    tier: {
      restart: { title_short: "Restart", scope: "Keep session and workspace." },
      session: { title_short: "Reset session", scope: "Clear session; keep workspace." },
      full: { title_short: "Full reset", scope: "Clear session and workspace." },
    },
    disabled_reason: { unavailable: "Unavailable" },
    cta: {
      restart: "Restart",
      session: "Reset session & restart",
      full: "Full reset & restart",
    },
    request_failed: "Restart request failed. Check the agent status and try again.",
    cancel: "Cancel",
  },
};

vi.mock("../../i18n/use-t", () => ({
  useT: () => ({
    t: (selector: (resources: typeof RESOURCES) => string, vars?: Record<string, unknown>) =>
      selector(RESOURCES).replace(/\{\{(\w+)\}\}/g, (_, key: string) =>
        String(vars?.[key] ?? `{{${key}}}`),
      ),
  }),
}));

import { AgentRestartModal } from "./agent-restart-modal";

function renderModal(onOpenChange = vi.fn()) {
  render(
    <AgentRestartModal
      agentId="agent-1"
      agentHandle="atlas"
      agentName="Atlas"
      open
      onOpenChange={onOpenChange}
    />,
  );
  return onOpenChange;
}

describe("AgentRestartModal", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    restart.isPending = false;
    restart.isError = false;
  });

  it("offers exactly Raft's restart, session, and full modes", () => {
    renderModal();

    expect(screen.getByTestId("restart-tier-restart")).toBeInTheDocument();
    expect(screen.getByTestId("restart-tier-session")).toBeInTheDocument();
    expect(screen.getByTestId("restart-tier-full")).toBeInTheDocument();
    expect(screen.getAllByRole("radio")).toHaveLength(3);
  });

  it("submits the selected mode and closes after acceptance", () => {
    const onOpenChange = vi.fn();
    restart.mutate.mockImplementation(
      (_mode: string, options: { onSuccess: () => void }) => options.onSuccess(),
    );
    renderModal(onOpenChange);

    fireEvent.click(screen.getByTestId("restart-tier-session"));
    fireEvent.click(screen.getByTestId("restart-modal-submit"));

    expect(restart.mutate).toHaveBeenCalledWith(
      "session",
      expect.objectContaining({ onSuccess: expect.any(Function) }),
    );
    expect(restart.refreshAfterRequest).toHaveBeenCalledOnce();
    expect(onOpenChange).toHaveBeenCalledWith(false);
  });

  it("renders request failure inline without a toast surface", () => {
    restart.isError = true;
    renderModal();

    expect(screen.getByRole("alert")).toHaveTextContent(
      "Restart request failed. Check the agent status and try again.",
    );
  });
});
