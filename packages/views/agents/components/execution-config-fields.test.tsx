// @vitest-environment jsdom

import { render, screen } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { describe, expect, it, vi } from "vitest";
import { ExecutionConfigFields } from "./execution-config-fields";

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
          runtimeOnline
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
});
