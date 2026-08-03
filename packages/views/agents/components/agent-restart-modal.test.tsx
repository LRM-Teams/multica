// @vitest-environment jsdom
import { render, screen, fireEvent } from "@testing-library/react";
import { describe, it, expect, vi, beforeEach } from "vitest";
import { I18nProvider } from "@multica/core/i18n/react";
import type {
  AgentLifecycleOperation,
  AgentLifecyclePreflight,
} from "@multica/core/types";
import enAgents from "../../locales/en/agents.json";

const mutate = vi.hoisted(() => vi.fn());
const reset = vi.hoisted(() => vi.fn());
const refreshAfterTerminal = vi.hoisted(() => vi.fn());
const lifecycleState = vi.hoisted(() => ({
  current: {
    preflight: null as AgentLifecyclePreflight | null,
    operation: null as AgentLifecycleOperation | null,
    isPending: false,
  },
}));

vi.mock("@multica/core/agents", async (importActual) => {
  const actual = await importActual<typeof import("@multica/core/agents")>();
  return {
    ...actual,
    useAgentLifecycle: () => ({
      preflight: lifecycleState.current.preflight,
      preflightLoading: false,
      start: { mutate, isPending: lifecycleState.current.isPending, reset },
      operation: lifecycleState.current.operation,
      isTerminal: false,
      reset,
      refreshAfterTerminal,
    }),
  };
});

import { AgentRestartModal } from "./agent-restart-modal";

const ALL_SUPPORTED: AgentLifecyclePreflight = {
  actions: {
    restart: { supported: true, execution_mode: "immediate" },
    reset_session_restart: { supported: true, execution_mode: "immediate" },
    full_reset_restart: { supported: true, execution_mode: "immediate" },
  },
};

function renderModal() {
  return render(
    <I18nProvider locale="en" resources={{ en: { agents: enAgents } }}>
      <AgentRestartModal
        agentId="a-1"
        agentHandle="atlas"
        agentName="Atlas"
        open
        onOpenChange={() => {}}
      />
    </I18nProvider>,
  );
}

beforeEach(() => {
  vi.clearAllMocks();
  lifecycleState.current = { preflight: ALL_SUPPORTED, operation: null, isPending: false };
});

describe("AgentRestartModal (#27 / #28 scope under each tier)", () => {
  it("renders three blocks with title + scope so each restart kind is clear", () => {
    renderModal();
    expect(screen.getByTestId("restart-tier-blocks")).toBeInTheDocument();
    expect(screen.getByTestId("restart-tier-restart")).toHaveTextContent("Restart");
    expect(screen.getByTestId("restart-tier-reset_session_restart")).toHaveTextContent(
      "Reset session",
    );
    expect(screen.getByTestId("restart-tier-full_reset_restart")).toHaveTextContent("Full reset");
    // Frank #28: each option must explain meaning (locale scope lines)
    expect(
      screen.getByText(/Restarts the process\. Keeps the session, workspace/i),
    ).toBeInTheDocument();
    expect(
      screen.getByText(/Clears the model session and context, then restarts/i),
    ).toBeInTheDocument();
    expect(
      screen.getByText(/Clears the session and permanently deletes the workspace/i),
    ).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Restart" })).toBeInTheDocument();
  });

  it("starts plain restart on primary CTA without changing selection", () => {
    renderModal();
    fireEvent.click(screen.getByRole("button", { name: "Restart" }));
    expect(mutate).toHaveBeenCalledWith("restart");
  });

  it("full reset: select Full and click CTA — no type-to-confirm (Frank)", () => {
    renderModal();
    expect(screen.queryByLabelText("Enter atlas")).not.toBeInTheDocument();
    fireEvent.click(screen.getByTestId("restart-tier-full_reset_restart"));
    // Still no type-handle field
    expect(screen.queryByLabelText("Enter atlas")).not.toBeInTheDocument();
    const cta = screen.getByRole("button", { name: "Full reset & restart" });
    expect(cta).toBeEnabled();
    fireEvent.click(cta);
    expect(mutate).toHaveBeenCalledWith("full_reset_restart");
  });

  it("disables unsupported tier with reason", () => {
    lifecycleState.current.preflight = {
      actions: {
        restart: { supported: true, execution_mode: "immediate" },
        reset_session_restart: { supported: true, execution_mode: "immediate" },
        full_reset_restart: {
          supported: false,
          disabled_reason: "agent_active",
          execution_mode: "immediate",
        },
      },
    };
    renderModal();
    expect(
      screen.getByText("Wait until the current task finishes, then try again."),
    ).toBeInTheDocument();
  });

  it("scheduled op is non-blocking — Done, no spinner lock", () => {
    lifecycleState.current.operation = {
      id: "op-sched",
      agent_id: "a-1",
      runtime_id: "rt-1",
      action_kind: "reset_session_restart",
      status: "scheduled",
      execution_mode: "after_current_run",
      created_at: "2026-07-28T00:00:00Z",
    };
    renderModal();
    expect(screen.getByRole("button", { name: "Done" })).toBeInTheDocument();
    expect(document.querySelector(".animate-spin")).toBeNull();
  });

  it("shows success + Done", () => {
    lifecycleState.current.operation = {
      id: "op-1",
      agent_id: "a-1",
      runtime_id: "rt-1",
      action_kind: "restart",
      status: "succeeded",
      execution_mode: "immediate",
      created_at: "2026-07-28T00:00:00Z",
    };
    renderModal();
    expect(screen.getByText("Done. The agent is back online.")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Done" })).toBeInTheDocument();
  });

  it("shows failure + Retry", () => {
    lifecycleState.current.operation = {
      id: "op-1",
      agent_id: "a-1",
      runtime_id: "rt-1",
      action_kind: "restart",
      status: "failed",
      execution_mode: "immediate",
      reason_code: "disk_full",
      created_at: "2026-07-28T00:00:00Z",
    };
    renderModal();
    expect(screen.getByText(/Restart failed: disk_full/)).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Retry" }));
    expect(mutate).toHaveBeenCalledWith("restart");
  });
});
