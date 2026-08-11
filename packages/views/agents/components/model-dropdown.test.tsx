// @vitest-environment jsdom

import { render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { RuntimeModel } from "@multica/core/types";
import { ModelDropdown } from "./model-dropdown";

let models: RuntimeModel[] = [];

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
  runtimeModelsOptions: (id: string | null) => ({ queryKey: ["runtime-models", id] }),
}));

vi.mock("../../i18n", () => ({
  useT: () => ({
    t: () => "translated",
  }),
}));

describe("ModelDropdown selected model label", () => {
  beforeEach(() => {
    models = [];
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
        runtimeOnline
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
        runtimeOnline={false}
        value="private/model-v7"
        onChange={vi.fn()}
      />,
    );

    expect(screen.getByTestId("model-dropdown-trigger")).toHaveTextContent(
      "private/model-v7",
    );
  });
});
