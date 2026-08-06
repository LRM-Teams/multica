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
          label: "Opus 5",
          default: true,
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
  }),
}));

vi.mock("@multica/core/runtimes", () => ({
  runtimeModelsOptions: (id: string | null) => ({ queryKey: ["models", id] }),
  deriveRuntimeHealth: () => "online",
}));

vi.mock("./inspector/runtime-picker", () => ({
  RuntimePicker: ({
    value,
    onChange,
  }: {
    value: string;
    onChange: (id: string) => void;
  }) => (
    <button
      type="button"
      data-testid="draft-runtime"
      onClick={() => onChange("rt-2")}
    >
      {value}
    </button>
  ),
}));

vi.mock("./inspector/model-picker", () => ({
  ModelPicker: ({
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

vi.mock("./inspector/thinking-picker", () => ({
  ThinkingPicker: ({
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
    save: "Save",
    cancel: "Cancel",
  },
  execution_config: {
    dialog_title: "Runtime config",
    dialog_description: "Edits stay local until you save.",
    dialog_saving: "Saving…",
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
  { id: "rt-1", status: "online" },
  { id: "rt-2", status: "online" },
] as AgentRuntime[];

const members = [] as MemberWithUser[];

describe("RuntimeConfigDialog (LRM-1351)", () => {


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
        runtimeOnline
        onSave={onSave}
      />,
    );

    fireEvent.click(screen.getByTestId("draft-model"));
    fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
    expect(onSave).not.toHaveBeenCalled();
    expect(onOpenChange).toHaveBeenCalledWith(false);
  });
});
