// @vitest-environment jsdom
import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { ReactNode } from "react";

const useQueryMock = vi.hoisted(() => vi.fn());
vi.mock("@tanstack/react-query", async (importActual) => {
  const actual = await importActual<typeof import("@tanstack/react-query")>();
  return {
    ...actual,
    useQuery: (...args: unknown[]) => useQueryMock(...args),
  };
});

vi.mock("../../i18n", () => ({
  useT: () => ({
    t: (sel: (d: Record<string, unknown>) => string) =>
      sel({
        model_dropdown: {
          label: "Model",
          select_runtime_first: "Select a runtime first",
          select_required: "Select a model",
          default_provider: "Default (provider)",
          runtime_offline_manual: "Runtime offline — enter manually",
          catalog_unavailable_hint: "Catalog unavailable — type a model ID",
          managed_by_runtime_title: "managed",
          managed_by_runtime_hint: "hint",
          discovery_failed: "discovery failed",
          clear_full: "Clear",
        },
        pickers: {
          model_search_placeholder: "Search",
          model_discovering: "Discovering…",
          model_empty_custom_hint: "Type a custom model ID",
          model_empty_with_dot: "No models",
          model_custom_input_placeholder: "Custom model ID",
          model_custom_row: "Custom model ID…",
        },
      } as never),
  }),
}));

import { ModelDropdown } from "./model-dropdown";

function wrap(ui: ReactNode) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(<QueryClientProvider client={qc}>{ui}</QueryClientProvider>);
}

beforeEach(() => {
  vi.clearAllMocks();
});

describe("ModelDropdown #124 offline freeform", () => {
  it("offline: soft catalog hint, never 'Runtime offline — enter manually' as trigger", () => {
    useQueryMock.mockReturnValue({
      data: undefined,
      isLoading: false,
      isError: false,
    });
    wrap(
      <ModelDropdown
        runtimeId="rt-1"
        runtimeOnline={false}
        value=""
        onChange={vi.fn()}
        required
      />,
    );
    expect(screen.getByTestId("model-dropdown-catalog-hint")).toHaveTextContent(
      /Catalog unavailable/i,
    );
    expect(screen.getByTestId("model-dropdown-trigger")).toHaveTextContent(
      "Select a model",
    );
    expect(screen.queryByText(/Runtime offline — enter manually/i)).toBeNull();
  });

  it("offline: opens freeform custom model row without catalog capability", () => {
    useQueryMock.mockReturnValue({
      data: undefined,
      isLoading: false,
      isError: false,
    });
    wrap(
      <ModelDropdown
        runtimeId="rt-1"
        runtimeOnline={false}
        value=""
        onChange={vi.fn()}
        required
      />,
    );
    fireEvent.click(screen.getByTestId("model-dropdown-trigger"));
    // CustomModelIdRow shows when freeform allowed (ellipsis row label)
    expect(screen.getByText("Custom model ID…")).toBeInTheDocument();
  });

  it("online: still queries catalog and can freeform when capability true", () => {
    useQueryMock.mockReturnValue({
      data: {
        supported: true,
        customModelIdSupported: true,
        models: [{ id: "claude-sonnet", label: "Sonnet", provider: "anthropic" }],
      },
      isLoading: false,
      isError: false,
    });
    wrap(
      <ModelDropdown
        runtimeId="rt-1"
        runtimeOnline
        value=""
        onChange={vi.fn()}
      />,
    );
    // no offline hint when online with catalog
    expect(screen.queryByTestId("model-dropdown-catalog-hint")).toBeNull();
    fireEvent.click(screen.getByTestId("model-dropdown-trigger"));
    expect(screen.getByText("Sonnet")).toBeInTheDocument();
  });
});
