// @vitest-environment jsdom

import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import type { Agent, AgentRuntime, MemberWithUser } from "@multica/core/types";
import { RuntimeConfigDialog } from "./runtime-config-dialog";

vi.mock("../../i18n", () => ({
  useT: () => ({
    t: (selector: (r: typeof RESOURCES) => string) => selector(RESOURCES),
  }),
}));

vi.mock("@tanstack/react-query", () => ({
  useQuery: () => ({
    data: {
      supported: true,
      thinkingDiscovery: true,
      models: [
        {
          id: "claude-opus-5",
          label: "Claude Opus 5",
          thinking: {
            supported_levels: [
              { value: "low", label: "Low" },
              { value: "high", label: "High" },
            ],
          },
        },
      ],
    },
    isLoading: false,
    isFetching: false,
  }),
  useQueryClient: () => ({
    prefetchQuery: vi.fn(),
    invalidateQueries: vi.fn(),
  }),
}));

vi.mock("@multica/core/runtimes", () => ({
  runtimeModelsOptions: (id: string | null) => ({ queryKey: ["models", id] }),
}));

vi.mock("../../runtimes/components/runtime-machines", () => ({
  buildRuntimeMachines: (runtimes: AgentRuntime[]) => [
    {
      id: "machine-1",
      daemonId: "daemon-1",
      title: "Test Mac",
      subtitle: null,
      deviceInfo: null,
      deviceName: null,
      cliVersion: null,
      mode: "local",
      section: "local",
      isCurrent: true,
      health: "online",
      runtimeHealth: null,
      updateError: null,
      daemonTargetVersion: null,
      runtimes,
      onlineCount: runtimes.length,
      issueCount: 0,
      runningCount: 0,
      queuedCount: 0,
      providerNames: [],
      lastSeenAt: null,
    },
  ],
}));

vi.mock("./computer-picker", () => ({
  ComputerPicker: ({
    selectedMachineId,
    onSelect,
  }: {
    selectedMachineId: string;
    onSelect: (id: string) => void;
  }) => (
    <button
      type="button"
      data-testid="draft-computer"
      onClick={() => onSelect("machine-1")}
    >
      {selectedMachineId || "none"}
    </button>
  ),
}));

vi.mock("./runtime-picker", () => ({
  RuntimePicker: ({
    selectedRuntimeId,
    onSelect,
  }: {
    selectedRuntimeId: string;
    onSelect: (id: string) => void;
  }) => (
    <button
      type="button"
      data-testid="draft-runtime"
      onClick={() => onSelect("rt-2")}
    >
      {selectedRuntimeId}
    </button>
  ),
}));

vi.mock("./model-dropdown", () => ({
  ModelDropdown: ({
    value,
    onChange,
  }: {
    value: string;
    onChange: (id: string) => void;
  }) => (
    <button
      type="button"
      data-testid="draft-model"
      onClick={() => onChange("claude-sonnet-5")}
    >
      {value || "default"}
    </button>
  ),
}));

vi.mock("./thinking-dropdown", () => ({
  ThinkingDropdown: ({
    value,
    onChange,
  }: {
    value: string;
    onChange: (id: string) => void;
  }) => (
    <button
      type="button"
      data-testid="draft-thinking"
      onClick={() => onChange("high")}
    >
      {value || "default"}
    </button>
  ),
}));

const RESOURCES = {
  inspector: {
    prop_runtime: "Runtime",
    prop_model: "Model",
    prop_thinking: "Thinking",
    prop_computer: "Computer",
    save: "Save",
    cancel: "Cancel",
  },
  execution_config: {
    dialog_title: "Runtime config",
    dialog_description: "Edits stay local until you save.",
    dialog_saving: "Saving…",
  },
  create_dialog: {
    runtime_label: "Runtime",
  },
};

const agent = {
  id: "agent-1",
  workspace_id: "ws-1",
  runtime_id: "rt-1",
  model: "claude-opus-5",
  thinking_level: "low",
} as Agent;

const runtimes = [
  {
    id: "rt-1",
    status: "online",
    daemon_id: "daemon-1",
    name: "Claude",
    provider: "claude",
    runtime_mode: "local",
  },
  {
    id: "rt-2",
    status: "online",
    daemon_id: "daemon-1",
    name: "Cursor",
    provider: "cursor",
    runtime_mode: "local",
  },
] as AgentRuntime[];

const members = [] as MemberWithUser[];

describe("RuntimeConfigDialog — Computer → Runtime → Model → Reasoning", () => {
  it("renders computer, runtime, model, and reasoning controls", () => {
    render(
      <RuntimeConfigDialog
        agent={agent}
        open
        onOpenChange={vi.fn()}
        runtimes={runtimes}
        members={members}
        currentUserId="user-1"
        onSave={vi.fn()}
      />,
    );

    expect(screen.getByTestId("execution-config-fields")).toBeInTheDocument();
    expect(screen.getByTestId("draft-computer")).toBeInTheDocument();
    expect(screen.getByTestId("draft-runtime")).toBeInTheDocument();
    expect(screen.getByTestId("draft-model")).toBeInTheDocument();
    expect(screen.getByTestId("draft-thinking")).toBeInTheDocument();
  });

  it("Cancel discards draft without calling onSave", async () => {
    const onSave = vi.fn();
    const onOpenChange = vi.fn();
    render(
      <RuntimeConfigDialog
        agent={agent}
        open
        onOpenChange={onOpenChange}
        runtimes={runtimes}
        members={members}
        currentUserId="user-1"
        onSave={onSave}
      />,
    );

    fireEvent.click(screen.getByTestId("draft-model"));
    fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
    expect(onSave).not.toHaveBeenCalled();
    expect(onOpenChange).toHaveBeenCalledWith(false);
  });

  it("Save patches runtime and clears model cascade when runtime changes", async () => {
    const onSave = vi.fn().mockResolvedValue(undefined);
    render(
      <RuntimeConfigDialog
        agent={agent}
        open
        onOpenChange={vi.fn()}
        runtimes={runtimes}
        members={members}
        currentUserId="user-1"
        onSave={onSave}
      />,
    );

    fireEvent.click(screen.getByTestId("draft-runtime"));
    // After runtime change, model is cleared — pick a model then save
    fireEvent.click(screen.getByTestId("draft-model"));
    fireEvent.click(screen.getByTestId("agent-runtime-config-save"));

    expect(onSave).toHaveBeenCalled();
    const firstCall = onSave.mock.calls[0];
    expect(firstCall).toBeDefined();
    const patch = firstCall![0] as Record<string, unknown>;
    expect(patch.runtime_id).toBe("rt-2");
    expect(patch.model).toBe("claude-sonnet-5");
  });
});
