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

describe("AgentRestartModal (#633)", () => {
  it("renders the three tiers with a default Restart CTA", () => {
    renderModal();
    expect(screen.getByText("Reset session & restart")).toBeInTheDocument();
    expect(screen.getByText("Full reset & restart")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Restart" })).toBeInTheDocument();
  });

  it("disables an unsupported tier and shows its reason (dormant / active)", () => {
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

  it("shows the after-current-run hint when a tier is scheduled", () => {
    lifecycleState.current.preflight = {
      actions: {
        restart: { supported: true, execution_mode: "after_current_run" },
        reset_session_restart: { supported: true, execution_mode: "immediate" },
        full_reset_restart: { supported: true, execution_mode: "immediate" },
      },
    };
    renderModal();
    expect(
      screen.getByText("Runs after the current task finishes."),
    ).toBeInTheDocument();
  });

  it("full reset requires typing the @handle before it can be submitted", () => {
    renderModal();
    fireEvent.click(screen.getByText("Full reset & restart"));

    const cta = screen.getByRole("button", { name: "Full reset & restart" });
    expect(cta).toBeDisabled();

    // Wrong text keeps it disabled.
    const input = screen.getByLabelText("Enter atlas");
    fireEvent.change(input, { target: { value: "wrong" } });
    expect(cta).toBeDisabled();

    // Exact handle enables it; submitting starts the action.
    fireEvent.change(input, { target: { value: "atlas" } });
    expect(cta).toBeEnabled();
    fireEvent.click(cta);
    expect(mutate).toHaveBeenCalledWith("full_reset_restart");
  });

  it("accepts the handle typed with a leading @ (no dead-end)", () => {
    renderModal();
    fireEvent.click(screen.getByText("Full reset & restart"));
    const cta = screen.getByRole("button", { name: "Full reset & restart" });
    fireEvent.change(screen.getByLabelText("Enter atlas"), {
      target: { value: "@atlas" },
    });
    expect(cta).toBeEnabled();
  });

  it("starts the selected action on submit", () => {
    renderModal();
    fireEvent.click(screen.getByRole("button", { name: "Restart" }));
    expect(mutate).toHaveBeenCalledWith("restart");
  });

  it("shows a success outcome + Done when the operation succeeds", () => {
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

  // Iris, 08-02: "identity/configuration/chat history/Issues are kept" was
  // said 3x in this modal (top description, full_reset's own scope, and the
  // full_reset confirm box) — say it once where it applies to all three
  // tiers, reference it nowhere else.
  it("states what's preserved exactly once — not repeated in the full_reset tier or its confirm box", () => {
    renderModal();
    fireEvent.click(screen.getByText("Full reset & restart"));
    const preservedFactMatches = screen.getAllByText(
      /identity, configuration, chat history, and Issues/i,
    );
    expect(preservedFactMatches).toHaveLength(1);
  });

  it("the full_reset confirm box states irreversibility instead of re-listing what's preserved", () => {
    renderModal();
    fireEvent.click(screen.getByText("Full reset & restart"));
    expect(screen.getByText(/can't be undone/i)).toBeInTheDocument();
  });

  it("shows the failure reason + Retry when the operation fails", () => {
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
    const retry = screen.getByRole("button", { name: "Retry" });
    fireEvent.click(retry);
    expect(reset).toHaveBeenCalled();
    expect(mutate).toHaveBeenCalledWith("restart");
  });
});
