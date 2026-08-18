// @vitest-environment jsdom

import { render, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { describe, expect, it, vi } from "vitest";
import { ExecutionConfigFields } from "./execution-config-fields";

const runtimeModelsOptionsMock = vi.hoisted(() => vi.fn());

vi.mock("@multica/core/runtimes", () => ({
  runtimeModelsOptions: (runtimeId: string | null) => {
    runtimeModelsOptionsMock(runtimeId);
    return {
      queryKey: ["runtime-models", runtimeId],
      queryFn: async () => ({ models: [] }),
    };
  },
}));

vi.mock("./computer-picker", () => ({
  ComputerPicker: ({ disabled }: { disabled?: boolean }) => (
    <button data-testid="computer" disabled={disabled} />
  ),
}));

vi.mock("./runtime-picker", () => ({
  RuntimePicker: ({ disabled }: { disabled?: boolean }) => (
    <button data-testid="runtime" disabled={disabled} />
  ),
}));

vi.mock("./model-dropdown", () => ({
  ModelDropdown: ({ disabled }: { disabled?: boolean }) => (
    <button data-testid="model" disabled={disabled} />
  ),
}));

vi.mock("./thinking-dropdown", () => ({
  ThinkingDropdown: ({ disabled }: { disabled?: boolean }) => (
    <button data-testid="thinking" disabled={disabled} />
  ),
}));

describe("ExecutionConfigFields disabled state", () => {
  it("disables the full Computer to Reasoning chain", () => {
    render(
      <QueryClientProvider client={new QueryClient()}>
        <ExecutionConfigFields
          runtimes={[]}
          members={[]}
          currentUserId="user-1"
          machineId="machine-1"
          onMachineSelect={vi.fn()}
          machineRuntimes={[]}
          runtimeId="runtime-1"
          onRuntimeSelect={vi.fn()}
          model="model-1"
          onModelChange={vi.fn()}
          thinkingLevel="high"
          onThinkingChange={vi.fn()}
          disabled
        />
      </QueryClientProvider>,
    );

    for (const id of ["computer", "runtime", "model", "thinking"]) {
      expect(screen.getByTestId(id)).toBeDisabled();
    }
  });

  it("prefetches the catalog as soon as a runtime is selected", async () => {
    runtimeModelsOptionsMock.mockClear();
    render(
      <QueryClientProvider client={new QueryClient()}>
        <ExecutionConfigFields
          runtimes={[]}
          members={[]}
          currentUserId="user-1"
          machineId="machine-1"
          onMachineSelect={vi.fn()}
          machineRuntimes={[]}
          runtimeId="runtime-1"
          onRuntimeSelect={vi.fn()}
          model=""
          onModelChange={vi.fn()}
          thinkingLevel=""
          onThinkingChange={vi.fn()}
        />
      </QueryClientProvider>,
    );

    await waitFor(() => {
      expect(runtimeModelsOptionsMock).toHaveBeenCalledWith("runtime-1");
    });
    expect(screen.getByTestId("thinking")).toBeEnabled();
  });
});
