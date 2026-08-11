// @vitest-environment jsdom
import type { ReactNode } from "react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { renderHook, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { useComputerReleaseVersion } from "./computer-metainfo";

function createWrapper() {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  return function Wrapper({ children }: { children: ReactNode }) {
    return (
      <QueryClientProvider client={queryClient}>
        {children}
      </QueryClientProvider>
    );
  };
}

describe("useComputerReleaseVersion", () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("keeps the configured Production release without fetching Test metainfo", () => {
    const fetchMock = vi.fn();
    vi.stubGlobal("fetch", fetchMock);

    const { result } = renderHook(
      () => useComputerReleaseVersion("production", " v0.4.23 "),
      { wrapper: createWrapper() },
    );

    expect(result.current).toBe("v0.4.23");
    expect(fetchMock).not.toHaveBeenCalled();
  });

  it("resolves Test to the exact canonical preview release", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue(
        new Response(
          JSON.stringify({
            schema_version: 1,
            environments: { test: { tag: "v0.4.24-alpha.13" } },
          }),
          { status: 200 },
        ),
      ),
    );

    const { result } = renderHook(
      () => useComputerReleaseVersion("test", "v0.4.24-alpha.1"),
      { wrapper: createWrapper() },
    );

    expect(result.current).toBe("test");
    await waitFor(() => expect(result.current).toBe("v0.4.24-alpha.13"));
  });

  it("keeps the valid Test selector when canonical metainfo is unavailable", async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response(null, { status: 503 }));
    vi.stubGlobal("fetch", fetchMock);

    const { result } = renderHook(
      () => useComputerReleaseVersion("test"),
      { wrapper: createWrapper() },
    );

    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(1));
    expect(result.current).toBe("test");
  });
});
