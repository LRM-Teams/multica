// @vitest-environment jsdom

import { fireEvent, render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

const useQueryMock = vi.hoisted(() => vi.fn());
vi.mock("@tanstack/react-query", async (importActual) => {
  const actual = await importActual<typeof import("@tanstack/react-query")>();
  return {
    ...actual,
    useQuery: (...args: unknown[]) => useQueryMock(...args),
  };
});

vi.mock("../../../i18n", () => ({
  useT: () => ({
    t: (selector: (resources: typeof RESOURCES) => string) => selector(RESOURCES),
  }),
}));

import { ModelPicker } from "./model-picker";

const RESOURCES = {
  filters: {
    placeholder: "Filter options",
  },
  pickers: {
    filter_options_aria: "Filter options",
    model_default: "Provider default",
    model_tooltip: "Model: {{value}}",
    model_search_placeholder: "Search models",
    model_discovering: "Discovering models…",
    model_empty: "No models found.",
    model_empty_custom_hint: "No models found — use a custom model ID.",
    model_clear_title: "Clear model override",
    model_clear: "Clear",
    model_managed_by_runtime: "Managed by runtime",
    model_custom_input_placeholder: "Custom model ID",
    model_custom_row: "Custom model ID…",
  },
};

function renderPicker() {
  render(
    <ModelPicker
      runtimeId="runtime-1"
      runtimeOnline
      value="gpt-5.6-sol"
      onChange={vi.fn()}
    />,
  );
  fireEvent.click(screen.getByRole("button", { name: /Model:/ }));
}

describe("ModelPicker catalog states (LRM-1387)", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("does not report an empty catalog before discovery has succeeded", () => {
    useQueryMock.mockReturnValue({
      data: undefined,
      isLoading: false,
      isPending: true,
      isSuccess: false,
    });

    renderPicker();

    expect(screen.queryByText("No models found.")).toBeNull();
    expect(screen.getByText("gpt-5.6-sol")).toBeInTheDocument();
  });

  it("reports the empty state after a successful empty catalog response", () => {
    useQueryMock.mockReturnValue({
      data: { models: [], supported: true, customModelIdSupported: false },
      isLoading: false,
      isPending: false,
      isSuccess: true,
    });

    renderPicker();

    expect(screen.getByText("No models found.")).toBeInTheDocument();
  });

  it("keeps the selected model available when the catalog is non-empty", () => {
    useQueryMock.mockReturnValue({
      data: {
        models: [
          { id: "gpt-5.6-sol", label: "GPT-5.6 Sol" },
          { id: "gpt-5.6-terra", label: "GPT-5.6 Terra" },
        ],
        supported: true,
        customModelIdSupported: false,
      },
      isLoading: false,
      isPending: false,
      isSuccess: true,
    });

    renderPicker();

    expect(screen.getByText("GPT-5.6 Sol")).toBeInTheDocument();
    expect(screen.getByText("GPT-5.6 Terra")).toBeInTheDocument();
    expect(screen.queryByText("No models found.")).toBeNull();
  });
});
