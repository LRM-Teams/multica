// @vitest-environment jsdom

import { render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { RuntimeModel } from "@multica/core/types";
import { ModelDropdown } from "./model-dropdown";

let models: RuntimeModel[] = [];
const runtimeModelsOptionsMock = vi.hoisted(() => vi.fn());

vi.mock("@tanstack/react-query", () => ({
  useQuery: () => ({
    data: {
      supported: true,
      customModelIdSupported: true,
      models,
    },
    isLoading: false,
    isFetching: false,
    isError: false,
  }),
  useQueryClient: () => ({
    invalidateQueries: vi.fn(),
  }),
}));

vi.mock("@multica/core/runtimes", () => ({
  runtimeModelsKeys: { forRuntime: (id: string) => ["runtime-models", id] },
  runtimeModelsOptions: (id: string | null) => {
    runtimeModelsOptionsMock(id);
    return { queryKey: ["runtime-models", id] };
  },
}));

vi.mock("../../i18n", () => ({
  useT: () => ({
    t: () => "translated",
  }),
}));

describe("ModelDropdown selected model label", () => {
  beforeEach(() => {
    models = [];
    runtimeModelsOptionsMock.mockClear();
  });

  it("shows the catalog model label instead of its provider", () => {
    models = [
      {
        id: "gpt-5.6-sol",
        label: "GPT-5.6 Sol",
        provider: "openai",
      },
    ];

    render(
      <ModelDropdown
        runtimeId="runtime-1"
        value="gpt-5.6-sol"
        onChange={vi.fn()}
      />,
    );

    expect(screen.getByTestId("model-dropdown-trigger")).toHaveTextContent(
      "GPT-5.6 Sol",
    );
    expect(screen.getByTestId("model-dropdown-trigger")).not.toHaveTextContent(
      "openai",
    );
  });

  it("preserves a custom model id when it is absent from the catalog", () => {
    render(
      <ModelDropdown
        runtimeId="runtime-1"
        value="private/model-v7"
        onChange={vi.fn()}
      />,
    );

    expect(screen.getByTestId("model-dropdown-trigger")).toHaveTextContent(
      "private/model-v7",
    );
  });

  it("truncates long model ids in the closed trigger (no overflow)", () => {
    const longId =
      "lenovo-deepseek-v4/DeepSeek-V4-Flash-0731-extra-long-suffix-that-must-ellipsis";
    render(
      <ModelDropdown
        runtimeId="runtime-1"
        value={longId}
        onChange={vi.fn()}
      />,
    );

    const trigger = screen.getByTestId("model-dropdown-trigger");
    expect(trigger).toHaveTextContent(longId);
    expect(trigger).toHaveAttribute("title", longId);
    // Flex button + overflow-hidden is what keeps long ids inside the dialog.
    expect(trigger.className).toMatch(/overflow-hidden/);
    expect(trigger.className).toMatch(/min-w-0/);
    expect(trigger.className).toMatch(/max-w-full/);
    const label = screen.getByTestId("model-dropdown-trigger-label");
    expect(label.className).toMatch(/truncate/);
    expect(label.className).toMatch(/min-w-0/);
  });

  it("starts catalog discovery whenever a runtime is selected", () => {
    render(
      <ModelDropdown
        runtimeId="runtime-1"
        value=""
        onChange={vi.fn()}
      />,
    );

    expect(runtimeModelsOptionsMock).toHaveBeenCalledWith("runtime-1");
    expect(screen.getByTestId("model-dropdown-rescan")).toBeEnabled();
  });
});
